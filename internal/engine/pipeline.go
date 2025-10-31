package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/miradorstack/mirador-rca/internal/config"
	"github.com/miradorstack/mirador-rca/internal/extractors"
	"github.com/miradorstack/mirador-rca/internal/models"
	"github.com/miradorstack/mirador-rca/internal/repo"
)

// CoreClient defines the mirador-core signal client behaviour used by the pipeline.
type CoreClient interface {
	FetchMetricSeries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.MetricPoint, error)
	FetchLogEntries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.LogEntry, error)
	FetchTraceSpans(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.TraceSpan, error)
	FetchServiceGraph(ctx context.Context, tenantID string, start, end time.Time) ([]repo.ServiceGraphEdge, error)
}

// WeaviateClient describes the Weaviate operations required for the pipeline.
type WeaviateClient interface {
	SimilarIncidents(ctx context.Context, tenantID string, symptoms []string, limit int) ([]models.CorrelationResult, error)
	StoreCorrelation(ctx context.Context, tenantID string, correlation models.CorrelationResult) error
}

// Pipeline orchestrates the phase-1 investigation flow.
type Pipeline struct {
	logger           *slog.Logger
	coreClient       CoreClient
	metricsExtractor *extractors.MetricExtractor
	logsExtractor    *extractors.LogsExtractor
	tracesExtractor  *extractors.TracesExtractor
	weaviate         WeaviateClient
	rulesEngine      *RuleEngine
	causalityEngine  *CausalityEngine
	// llmClient is optional and, when set, used to generate a natural-language
	// summary of the correlation result if enabled in runtime config.
	llmClient interface {
		Summarize(ctx context.Context, prompt string) (string, error)
	}
}

// Signals captures the raw inputs required for analysis.
type Signals struct {
	ServiceGraph []repo.ServiceGraphEdge
	Metrics      []repo.MetricPoint
	Logs         []repo.LogEntry
	Traces       []repo.TraceSpan
}

// NewPipeline constructs a new investigation pipeline.
func NewPipeline(
	logger *slog.Logger,
	coreClient CoreClient,
	weaviate WeaviateClient,
	rulesEngine *RuleEngine,
	causalityEngine *CausalityEngine,
	metricsExtractor *extractors.MetricExtractor,
	logsExtractor *extractors.LogsExtractor,
	tracesExtractor *extractors.TracesExtractor,
) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	if metricsExtractor == nil {
		metricsExtractor = extractors.NewMetricExtractor()
	}
	if logsExtractor == nil {
		logsExtractor = extractors.NewLogsExtractor()
	}
	if tracesExtractor == nil {
		tracesExtractor = extractors.NewTracesExtractor()
	}

	return &Pipeline{
		logger:           logger,
		coreClient:       coreClient,
		metricsExtractor: metricsExtractor,
		logsExtractor:    logsExtractor,
		tracesExtractor:  tracesExtractor,
		weaviate:         weaviate,
		rulesEngine:      rulesEngine,
		causalityEngine:  causalityEngine,
	}
}

// SetLLMClient attaches an LLM client to the pipeline. The client may be nil to disable LLM usage.
func (p *Pipeline) SetLLMClient(c interface {
	Summarize(ctx context.Context, prompt string) (string, error)
}) {
	p.llmClient = c
}

// Investigate executes the anomaly detection + ranking flow and returns a correlation result.
func (p *Pipeline) Investigate(ctx context.Context, req models.InvestigationRequest) (models.CorrelationResult, error) {
	if p.coreClient == nil {
		return models.CorrelationResult{}, fmt.Errorf("core client not configured")
	}

	service := p.DetermineService(req)
	signals, err := p.FetchSignals(ctx, req, service)
	if err != nil {
		return models.CorrelationResult{}, err
	}

	result, err := p.Analyze(ctx, req, service, signals)
	if err != nil {
		return models.CorrelationResult{}, err
	}
	p.PersistResult(ctx, req.TenantID, result)
	return result, nil
}

// DetermineService infers the primary service under investigation.
func (p *Pipeline) DetermineService(req models.InvestigationRequest) string {
	service := firstNonEmpty(req.AffectedServices...)
	if service == "" && len(req.Symptoms) > 0 {
		service = req.Symptoms[0]
	}
	if service == "" {
		service = "unknown-service"
	}
	return service
}

// FetchSignals retrieves metrics/logs/traces and the optional service graph from mirador-core.
func (p *Pipeline) FetchSignals(ctx context.Context, req models.InvestigationRequest, service string) (Signals, error) {
	var sig Signals
	if p.coreClient == nil {
		return sig, fmt.Errorf("core client not configured")
	}

	graph, err := p.coreClient.FetchServiceGraph(ctx, req.TenantID, req.TimeRange.Start, req.TimeRange.End)
	if err != nil {
		p.logger.Warn("service graph fetch failed", slog.Any("error", err))
	} else {
		sig.ServiceGraph = graph
	}

	metrics, err := p.coreClient.FetchMetricSeries(ctx, req.TenantID, service, req.TimeRange.Start, req.TimeRange.End)
	if err != nil {
		return sig, fmt.Errorf("fetch metrics: %w", err)
	}
	logs, err := p.coreClient.FetchLogEntries(ctx, req.TenantID, service, req.TimeRange.Start, req.TimeRange.End)
	if err != nil {
		return sig, fmt.Errorf("fetch logs: %w", err)
	}
	spans, err := p.coreClient.FetchTraceSpans(ctx, req.TenantID, service, req.TimeRange.Start, req.TimeRange.End)
	if err != nil {
		return sig, fmt.Errorf("fetch traces: %w", err)
	}

	sig.Metrics = metrics
	sig.Logs = logs
	sig.Traces = spans
	return sig, nil
}

// Analyze performs anomaly detection, causality checks, and recommendation assembly.
func (p *Pipeline) Analyze(ctx context.Context, req models.InvestigationRequest, service string, signals Signals) (models.CorrelationResult, error) {
	metricAnomalies := p.metricsExtractor.Detect(signals.Metrics, req.AnomalyThreshold)
	logAnomalies := p.logsExtractor.Detect(signals.Logs)
	traceAnomalies := p.tracesExtractor.Detect(signals.Traces)

	anchors := p.buildAnchors(service, metricAnomalies, logAnomalies, traceAnomalies)
	timeline := p.buildTimeline(metricAnomalies, logAnomalies, traceAnomalies)

	confidence := p.computeConfidence(metricAnomalies, logAnomalies, traceAnomalies)
	rootCause := deriveRootCause(service, anchors)

	causalityScore := 0.0
	var causalityResult CausalityResult
	if p.causalityEngine != nil {
		causalityResult = p.causalityEngine.Evaluate(service, timeline, signals.ServiceGraph)
		causalityScore = causalityResult.Score
		if len(causalityResult.Notes) > 0 {
			for _, note := range causalityResult.Notes {
				p.logger.Debug("causality note", slog.String("note", note))
			}
		}
	}

	recommendations := p.fetchRecommendations(ctx, req, anchors, timeline)
	affected := uniqueStrings(append([]string{service}, req.AffectedServices...))
	affected = uniqueStrings(append(affected, neighborServices(signals.ServiceGraph, service)...))

	if causalityResult.SuggestedService != "" && !strings.EqualFold(causalityResult.SuggestedService, service) {
		affected = uniqueStrings(append(affected, causalityResult.SuggestedService))
		rootCause = fmt.Sprintf("%s: upstream influence on %s", causalityResult.SuggestedService, service)
		suggestedEvent := models.TimelineEvent{
			Time:         rootEventTime(causalityResult.SuggestedService, timeline).Add(-500 * time.Millisecond),
			Event:        fmt.Sprintf("Causality: %s precedes %s", causalityResult.SuggestedService, service),
			Service:      causalityResult.SuggestedService,
			Severity:     models.SeverityMedium,
			AnomalyScore: 0,
			DataSource:   models.DataTypeTraces,
		}
		timeline = append(timeline, suggestedEvent)
	}

	timeline = p.appendTopologyEvents(timeline, service, signals.ServiceGraph)

	result := models.CorrelationResult{
		CorrelationID:    fmt.Sprintf("corr-%d", time.Now().UnixNano()),
		IncidentID:       req.IncidentID,
		RootCause:        rootCause,
		Confidence:       calibrateConfidence(confidence, causalityScore),
		AffectedServices: affected,
		Recommendations:  recommendations,
		RedAnchors:       anchors,
		Timeline:         timeline,
		CreatedAt:        time.Now().UTC(),
	}

	// If an LLM client has been attached and runtime config enables LLM augmentation,
	// build a concise prompt and attempt to generate an LLMSummary. Failures are
	// non-fatal: we log and continue returning the result without an LLMSummary.
	if p.llmClient != nil {
		rt := config.GetRuntimeConfig()
		if rt != nil && rt.LLM.Enabled {
			// Create a short prompt containing root cause and top anchors.
			var b strings.Builder
			b.WriteString("Summarize the investigation result in 2-3 sentences.\n")
			b.WriteString("Root cause: ")
			b.WriteString(result.RootCause)
			b.WriteString("\nTop anchors:\n")
			for i, a := range result.RedAnchors {
				if i >= 3 {
					break
				}
				b.WriteString("- ")
				b.WriteString(a.Service)
				b.WriteString(" ")
				b.WriteString(a.Selector)
				b.WriteString(" score=")
				b.WriteString(fmt.Sprintf("%.2f", a.AnomalyScore))
				b.WriteString("\n")
			}
			timeout := rt.LLM.Timeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			summary, err := p.llmClient.Summarize(cctx, b.String())
			if err != nil {
				p.logger.Warn("llm summarization failed", slog.Any("error", err))
			} else {
				result.LLMSummary = summary
			}
		}
	}

	return result, nil
}

// PersistResult stores the correlation outcome in the historical repository (best-effort).
func (p *Pipeline) PersistResult(ctx context.Context, tenantID string, result models.CorrelationResult) {
	if p.weaviate == nil {
		return
	}
	if err := p.weaviate.StoreCorrelation(ctx, tenantID, result); err != nil {
		p.logger.Warn("failed to persist correlation", slog.Any("error", err))
	}
}

func (p *Pipeline) buildAnchors(service string, metricAnoms []extractors.MetricAnomaly, logAnoms []extractors.LogAnomaly, traceAnoms []extractors.TraceAnomaly) []models.RedAnchor {
	anchors := make([]models.RedAnchor, 0, len(metricAnoms)+len(logAnoms)+len(traceAnoms))

	for _, m := range metricAnoms {
		anchors = append(anchors, models.RedAnchor{
			Service:      service,
			Selector:     "metrics:cpu_usage",
			DataType:     models.DataTypeMetrics,
			Timestamp:    m.Timestamp,
			AnomalyScore: m.Score,
			Threshold:    m.Threshold,
		})
	}

	for _, l := range logAnoms {
		anchors = append(anchors, models.RedAnchor{
			Service:      service,
			Selector:     fmt.Sprintf("logs:%s", l.Severity),
			DataType:     models.DataTypeLogs,
			Timestamp:    l.Timestamp,
			AnomalyScore: l.Score,
			Threshold:    3,
		})
	}

	for _, t := range traceAnoms {
		anchors = append(anchors, models.RedAnchor{
			Service:      t.Span.Service,
			Selector:     fmt.Sprintf("trace:%s", t.Span.Operation),
			DataType:     models.DataTypeTraces,
			Timestamp:    t.Span.Timestamp,
			AnomalyScore: t.Score,
			Threshold:    2,
		})
	}

	sort.SliceStable(anchors, func(i, j int) bool {
		return anchors[i].AnomalyScore > anchors[j].AnomalyScore
	})

	if len(anchors) > 5 {
		anchors = anchors[:5]
	}

	return anchors
}

func (p *Pipeline) buildTimeline(metricAnoms []extractors.MetricAnomaly, logAnoms []extractors.LogAnomaly, traceAnoms []extractors.TraceAnomaly) []models.TimelineEvent {
	timeline := make([]models.TimelineEvent, 0, len(metricAnoms)+len(logAnoms)+len(traceAnoms))

	for _, m := range metricAnoms {
		timeline = append(timeline, models.TimelineEvent{
			Time:         m.Timestamp,
			Event:        "Metric anomaly detected",
			Service:      "",
			Severity:     severityFromScore(m.Score),
			AnomalyScore: m.Score,
			DataSource:   models.DataTypeMetrics,
		})
	}

	for _, l := range logAnoms {
		timeline = append(timeline, models.TimelineEvent{
			Time:         l.Timestamp,
			Event:        fmt.Sprintf("Log spike (%s)", l.Severity),
			Service:      "",
			Severity:     severityFromScore(l.Score),
			AnomalyScore: l.Score,
			DataSource:   models.DataTypeLogs,
		})
	}

	for _, t := range traceAnoms {
		severity := severityFromScore(t.Score)
		if t.Span.Status == "error" {
			severity = models.SeverityHigh
		}
		timeline = append(timeline, models.TimelineEvent{
			Time:         t.Span.Timestamp,
			Event:        fmt.Sprintf("Slow span: %s", t.Span.Operation),
			Service:      t.Span.Service,
			Severity:     severity,
			AnomalyScore: t.Score,
			DataSource:   models.DataTypeTraces,
		})
	}

	sort.Slice(timeline, func(i, j int) bool {
		return timeline[i].Time.Before(timeline[j].Time)
	})

	if len(timeline) > 10 {
		timeline = timeline[:10]
	}

	return timeline
}

func (p *Pipeline) computeConfidence(metricAnoms []extractors.MetricAnomaly, logAnoms []extractors.LogAnomaly, traceAnoms []extractors.TraceAnomaly) float64 {
	confidence := 0.0

	if len(metricAnoms) > 0 {
		confidence += 0.25 + clamp(maxMetricScore(metricAnoms)/8.0, 0, 0.25)
	}
	if len(logAnoms) > 0 {
		confidence += 0.25 + clamp(maxLogScore(logAnoms)/6.0, 0, 0.2)
	}
	if len(traceAnoms) > 0 {
		confidence += 0.25 + clamp(maxTraceScore(traceAnoms)/6.0, 0, 0.2)
	}

	if confidence > 1 {
		confidence = 1
	}
	return confidence
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (p *Pipeline) fetchRecommendations(ctx context.Context, req models.InvestigationRequest, anchors []models.RedAnchor, timeline []models.TimelineEvent) []string {
	if p.weaviate == nil {
		return p.recommendFromRules(req, anchors, timeline)
	}

	results, err := p.weaviate.SimilarIncidents(ctx, req.TenantID, req.Symptoms, 3)
	if err != nil || len(results) == 0 {
		return p.recommendFromRules(req, anchors, timeline)
	}

	recs := results[0].Recommendations
	if len(recs) == 0 {
		return p.recommendFromRules(req, anchors, timeline)
	}

	return recs
}

func (p *Pipeline) recommendFromRules(req models.InvestigationRequest, anchors []models.RedAnchor, timeline []models.TimelineEvent) []string {
	if p.rulesEngine != nil {
		if recs := p.rulesEngine.Recommend(req, anchors, timeline); len(recs) > 0 {
			return recs
		}
	}
	return defaultRecommendations()
}

func defaultRecommendations() []string {
	return []string{
		"Review recent deployments for regressions",
		"Check upstream dependencies for correlated errors",
	}
}

func severityFromScore(score float64) models.Severity {
	switch {
	case score >= 4:
		return models.SeverityCritical
	case score >= 3:
		return models.SeverityHigh
	case score >= 2:
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}

func deriveRootCause(service string, anchors []models.RedAnchor) string {
	if len(anchors) == 0 {
		return fmt.Sprintf("%s: no dominant anchor", service)
	}
	return fmt.Sprintf("%s: %s anomaly", anchors[0].Service, anchors[0].Selector)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func maxMetricScore(anoms []extractors.MetricAnomaly) float64 {
	max := 0.0
	for _, a := range anoms {
		if a.Score > max {
			max = a.Score
		}
	}
	return max
}

func maxLogScore(anoms []extractors.LogAnomaly) float64 {
	max := 0.0
	for _, a := range anoms {
		if a.Score > max {
			max = a.Score
		}
	}
	return max
}

func maxTraceScore(anoms []extractors.TraceAnomaly) float64 {
	max := 0.0
	for _, a := range anoms {
		if a.Score > max {
			max = a.Score
		}
	}
	return max
}

func neighborServices(edges []repo.ServiceGraphEdge, service string) []string {
	set := make(map[string]struct{})
	for _, edge := range edges {
		if edge.Source == service {
			set[edge.Target] = struct{}{}
		}
		if edge.Target == service {
			set[edge.Source] = struct{}{}
		}
	}
	neighbors := make([]string, 0, len(set))
	for svc := range set {
		neighbors = append(neighbors, svc)
	}
	return neighbors
}

func (p *Pipeline) appendTopologyEvents(timeline []models.TimelineEvent, service string, edges []repo.ServiceGraphEdge) []models.TimelineEvent {
	if len(edges) == 0 {
		return timeline
	}
	related := make([]repo.ServiceGraphEdge, 0)
	for _, edge := range edges {
		if edge.Source == service || edge.Target == service {
			related = append(related, edge)
		}
	}
	if len(related) == 0 {
		return timeline
	}
	sort.Slice(related, func(i, j int) bool {
		return related[i].CallRate > related[j].CallRate
	})
	limit := 2
	if len(related) < limit {
		limit = len(related)
	}
	rootTime := rootEventTime(service, timeline)
	if rootTime.IsZero() {
		rootTime = time.Now().UTC()
	}
	for i := 0; i < limit; i++ {
		edge := related[i]
		event := models.TimelineEvent{
			Time:         rootTime,
			Severity:     models.SeverityLow,
			AnomalyScore: 0,
			DataSource:   models.DataTypeTraces,
		}
		if edge.Target == service {
			event.Service = edge.Source
			event.Event = fmt.Sprintf("Service graph: upstream %s -> %s", edge.Source, edge.Target)
			event.Time = rootTime.Add(-500 * time.Millisecond)
		} else {
			event.Service = edge.Target
			event.Event = fmt.Sprintf("Service graph: %s -> %s", edge.Source, edge.Target)
			event.Time = rootTime.Add(500 * time.Millisecond)
		}
		if edge.ErrorRate > 0 {
			event.Event += fmt.Sprintf(" (error rate %.2f%%)", edge.ErrorRate)
		}
		timeline = append(timeline, event)
	}
	sort.Slice(timeline, func(i, j int) bool {
		return timeline[i].Time.Before(timeline[j].Time)
	})
	return timeline
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func calibrateConfidence(base, causality float64) float64 {
	base = clamp(base, 0, 1)
	if causality <= 0 {
		return clamp(base*0.7, 0, 1)
	}
	return clamp(base*0.6+causality*0.4, 0, 1)
}
