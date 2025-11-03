package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/miradorstack/mirador-rca/internal/cache"
)

// MetricPoint represents a single metric sample returned by mirador-core.
type MetricPoint struct {
	Timestamp time.Time
	Value     float64
}

// LogEntry represents aggregated log information for anomaly detection.
type LogEntry struct {
	Timestamp time.Time
	Message   string
	Severity  string
	Count     int
}

// TraceSpan captures essential fields from a trace span.
type TraceSpan struct {
	TraceID   string
	SpanID    string
	Service   string
	Operation string
	Duration  time.Duration
	Status    string
	Timestamp time.Time
}

// ServiceGraphEdge represents a dependency edge between two services.
type ServiceGraphEdge struct {
	Source    string
	Target    string
	CallRate  float64
	ErrorRate float64
}

// MiradorCoreClient wraps mirador-core RCA helper APIs for signals.
type MiradorCoreClient struct {
	baseURL            string
	metricsPath        string
	logsPath           string
	tracesPath         string
	serviceGraphPath   string
	correlationPath    string
	correlationEnabled bool
	httpClient         *http.Client
	cache              cache.Provider
	serviceGraphTTL    time.Duration
	correlationTTL     time.Duration
}

// NewMiradorCoreClient constructs a client targeting the configured mirador-core instance.
func NewMiradorCoreClient(baseURL, metricsPath, logsPath, tracesPath, serviceGraphPath, correlationPath string, correlationEnabled bool, timeout time.Duration, cacheProvider cache.Provider, serviceGraphTTL, correlationTTL time.Duration) *MiradorCoreClient {
	if cacheProvider == nil {
		cacheProvider = cache.NoopProvider{}
	}
	return &MiradorCoreClient{
		baseURL:            strings.TrimRight(baseURL, "/"),
		metricsPath:        metricsPath,
		logsPath:           logsPath,
		tracesPath:         tracesPath,
		serviceGraphPath:   serviceGraphPath,
		correlationPath:    correlationPath,
		correlationEnabled: correlationEnabled,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		cache:           cacheProvider,
		serviceGraphTTL: serviceGraphTTL,
		correlationTTL:  correlationTTL,
	}
}

// FetchMetricSeries queries mirador-core for metric samples.
func (c *MiradorCoreClient) FetchMetricSeries(ctx context.Context, tenantID, service string, start, end time.Time) ([]MetricPoint, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	payload := map[string]interface{}{
		"tenant_id": tenantID,
		"service":   service,
		"start":     start.Format(time.RFC3339),
		"end":       end.Format(time.RFC3339),
	}

	var response struct {
		Series []struct {
			Timestamp time.Time `json:"timestamp"`
			Value     float64   `json:"value"`
		} `json:"series"`
	}

	if err := c.postJSON(ctx, c.metricsURL(), payload, &response); err != nil {
		return nil, fmt.Errorf("mirador-core metrics request failed: %w", err)
	}

	points := make([]MetricPoint, 0, len(response.Series))
	for _, sample := range response.Series {
		points = append(points, MetricPoint{Timestamp: sample.Timestamp, Value: sample.Value})
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("mirador-core metrics returned no samples")
	}
	return points, nil
}

// FetchLogEntries queries mirador-core for log aggregates.
func (c *MiradorCoreClient) FetchLogEntries(ctx context.Context, tenantID, service string, start, end time.Time) ([]LogEntry, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	payload := map[string]interface{}{
		"tenant_id": tenantID,
		"service":   service,
		"start":     start.Format(time.RFC3339),
		"end":       end.Format(time.RFC3339),
	}

	var response struct {
		Entries []struct {
			Timestamp time.Time `json:"timestamp"`
			Message   string    `json:"message"`
			Severity  string    `json:"severity"`
			Count     int       `json:"count"`
		} `json:"entries"`
	}

	if err := c.postJSON(ctx, c.logsURL(), payload, &response); err != nil {
		return nil, fmt.Errorf("mirador-core logs request failed: %w", err)
	}

	entries := make([]LogEntry, 0, len(response.Entries))
	for _, e := range response.Entries {
		entries = append(entries, LogEntry{
			Timestamp: e.Timestamp,
			Message:   e.Message,
			Severity:  e.Severity,
			Count:     e.Count,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("mirador-core logs returned no entries")
	}
	return entries, nil
}

// FetchTraceSpans queries mirador-core for trace span anomalies.
func (c *MiradorCoreClient) FetchTraceSpans(ctx context.Context, tenantID, service string, start, end time.Time) ([]TraceSpan, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	payload := map[string]interface{}{
		"tenant_id": tenantID,
		"service":   service,
		"start":     start.Format(time.RFC3339),
		"end":       end.Format(time.RFC3339),
	}

	var response struct {
		Spans []struct {
			TraceID    string    `json:"trace_id"`
			SpanID     string    `json:"span_id"`
			Service    string    `json:"service"`
			Operation  string    `json:"operation"`
			DurationMs float64   `json:"duration_ms"`
			Status     string    `json:"status"`
			Timestamp  time.Time `json:"timestamp"`
		} `json:"spans"`
	}

	if err := c.postJSON(ctx, c.tracesURL(), payload, &response); err != nil {
		return nil, fmt.Errorf("mirador-core traces request failed: %w", err)
	}

	spans := make([]TraceSpan, 0, len(response.Spans))
	for _, span := range response.Spans {
		duration := time.Duration(span.DurationMs * float64(time.Millisecond))
		spans = append(spans, TraceSpan{
			TraceID:   span.TraceID,
			SpanID:    span.SpanID,
			Service:   firstNonEmpty(span.Service, service),
			Operation: span.Operation,
			Duration:  duration,
			Status:    span.Status,
			Timestamp: span.Timestamp,
		})
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("mirador-core traces returned no spans")
	}
	return spans, nil
}

// FetchServiceGraph retrieves service dependency edges derived from servicegraph metrics.
func (c *MiradorCoreClient) FetchServiceGraph(ctx context.Context, tenantID string, start, end time.Time) ([]ServiceGraphEdge, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	cacheKey := ""
	if c.serviceGraphTTL > 0 {
		cacheKey = serviceGraphCacheKey(tenantID, start, end)
		if data, err := c.cache.Get(ctx, cacheKey); err == nil {
			var cached []ServiceGraphEdge
			if err := json.Unmarshal(data, &cached); err == nil {
				return cached, nil
			}
		}
	}

	payload := map[string]interface{}{
		"tenant_id": tenantID,
		"start":     start.Format(time.RFC3339),
		"end":       end.Format(time.RFC3339),
	}

	var response struct {
		Edges []struct {
			Source    string  `json:"source"`
			Target    string  `json:"target"`
			CallRate  float64 `json:"call_rate"`
			ErrorRate float64 `json:"error_rate"`
		} `json:"edges"`
	}

	if err := c.postJSON(ctx, c.serviceGraphURL(), payload, &response); err != nil {
		return nil, fmt.Errorf("mirador-core service graph request failed: %w", err)
	}

	edges := make([]ServiceGraphEdge, 0, len(response.Edges))
	for _, edge := range response.Edges {
		edges = append(edges, ServiceGraphEdge{
			Source:    edge.Source,
			Target:    edge.Target,
			CallRate:  edge.CallRate,
			ErrorRate: edge.ErrorRate,
		})
	}

	if c.serviceGraphTTL > 0 && cacheKey != "" {
		if payload, err := json.Marshal(edges); err == nil {
			_ = c.cache.Set(ctx, cacheKey, payload, c.serviceGraphTTL)
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("mirador-core service graph returned no edges")
	}
	return edges, nil
}

// ExecuteCorrelationQuery executes a correlation query using mirador-core's unified correlation API
func (c *MiradorCoreClient) ExecuteCorrelationQuery(ctx context.Context, tenantID string, query map[string]interface{}) (map[string]interface{}, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	// Add tenant ID to the query
	query["tenant_id"] = tenantID

	var response map[string]interface{}

	if err := c.postJSON(ctx, c.correlationURL(), query, &response); err != nil {
		return nil, fmt.Errorf("mirador-core correlation request failed: %w", err)
	}

	return response, nil
}

// ExecuteUnifiedQuery executes a unified query using mirador-core's unified query API
func (c *MiradorCoreClient) ExecuteUnifiedQuery(ctx context.Context, tenantID string, query map[string]interface{}) (map[string]interface{}, error) {
	if c == nil {
		return nil, fmt.Errorf("mirador-core client not initialised")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("mirador-core base URL not configured")
	}

	// Add tenant ID to the query
	query["tenant_id"] = tenantID

	var response map[string]interface{}

	// Use unified query endpoint
	unifiedQueryURL := c.resolvePath("/api/v1/unified/query")
	if err := c.postJSON(ctx, unifiedQueryURL, query, &response); err != nil {
		return nil, fmt.Errorf("mirador-core unified query request failed: %w", err)
	}

	return response, nil
}

// IsCorrelationEnabled returns whether correlation features are enabled for this client
func (c *MiradorCoreClient) IsCorrelationEnabled() bool {
	return c.correlationEnabled
}

func serviceGraphCacheKey(tenantID string, start, end time.Time) string {
	return fmt.Sprintf("servicegraph:%s:%d:%d", tenantID, start.Unix(), end.Unix())
}

func (c *MiradorCoreClient) metricsURL() string      { return c.resolvePath(c.metricsPath) }
func (c *MiradorCoreClient) logsURL() string         { return c.resolvePath(c.logsPath) }
func (c *MiradorCoreClient) tracesURL() string       { return c.resolvePath(c.tracesPath) }
func (c *MiradorCoreClient) serviceGraphURL() string { return c.resolvePath(c.serviceGraphPath) }
func (c *MiradorCoreClient) correlationURL() string  { return c.resolvePath(c.correlationPath) }

func (c *MiradorCoreClient) resolvePath(p string) string {
	if c.baseURL == "" {
		return ""
	}
	cleaned := "/" + strings.TrimLeft(p, "/")
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL + cleaned
	}
	u.Path = path.Join(u.Path, cleaned)
	return u.String()
}

func (c *MiradorCoreClient) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	if endpoint == "" {
		return fmt.Errorf("empty endpoint")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mirador-core returned %s", resp.Status)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func syntheticMetricSeries(start, end time.Time) []MetricPoint { //nolint:unused
	if start.IsZero() || end.IsZero() || !end.After(start) {
		end = time.Now()
		start = end.Add(-15 * time.Minute)
	}
	step := end.Sub(start) / 15
	if step <= 0 {
		step = time.Minute
	}
	series := make([]MetricPoint, 0, 15)
	for i := 0; i < 15; i++ {
		ts := start.Add(time.Duration(i) * step)
		value := 0.5 + 0.05*float64(i)
		if i > 10 {
			value += 0.3
		}
		series = append(series, MetricPoint{Timestamp: ts, Value: value})
	}
	return series
}

func syntheticLogEntries(start, end time.Time) []LogEntry { //nolint:unused
	if start.IsZero() || end.IsZero() || !end.After(start) {
		end = time.Now()
		start = end.Add(-15 * time.Minute)
	}
	step := end.Sub(start) / 5
	if step <= 0 {
		step = 3 * time.Minute
	}
	entries := make([]LogEntry, 0, 5)
	for i := 0; i < 5; i++ {
		ts := start.Add(time.Duration(i) * step)
		severity := "info"
		count := 20 + i*2
		if i >= 3 {
			severity = "error"
			count += 12
		}
		entries = append(entries, LogEntry{Timestamp: ts, Message: "synthetic log", Severity: severity, Count: count})
	}
	return entries
}
