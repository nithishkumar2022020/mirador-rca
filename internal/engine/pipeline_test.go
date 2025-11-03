package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/miradorstack/mirador-rca/internal/extractors"
	"github.com/miradorstack/mirador-rca/internal/models"
	"github.com/miradorstack/mirador-rca/internal/repo"
)

type fakeCoreClient struct {
	metrics []repo.MetricPoint
	logs    []repo.LogEntry
	traces  []repo.TraceSpan
	graph   []repo.ServiceGraphEdge
}

func (f *fakeCoreClient) FetchMetricSeries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.MetricPoint, error) {
	return f.metrics, nil
}

func (f *fakeCoreClient) FetchLogEntries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.LogEntry, error) {
	return f.logs, nil
}

func (f *fakeCoreClient) FetchTraceSpans(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.TraceSpan, error) {
	return f.traces, nil
}

func (f *fakeCoreClient) FetchServiceGraph(ctx context.Context, tenantID string, start, end time.Time) ([]repo.ServiceGraphEdge, error) {
	return f.graph, nil
}

func (f *fakeCoreClient) ExecuteCorrelationQuery(ctx context.Context, tenantID string, query map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		"confidence_score": 0.8,
		"insights": []interface{}{
			map[string]interface{}{
				"event": "Cross-service correlation detected",
			},
		},
	}, nil
}

func (f *fakeCoreClient) ExecuteUnifiedQuery(ctx context.Context, tenantID string, query map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		"results": []interface{}{},
	}, nil
}

func (f *fakeCoreClient) IsCorrelationEnabled() bool {
	return true
}

type fakeWeaviate struct {
	stored int
}

func (f *fakeWeaviate) SimilarIncidents(ctx context.Context, tenantID string, symptoms []string, limit int) ([]models.CorrelationResult, error) {
	return []models.CorrelationResult{
		{
			Recommendations: []string{"Check caching layer", "Verify deployment"},
		},
	}, nil
}

func (f *fakeWeaviate) StoreCorrelation(ctx context.Context, tenantID string, correlation models.CorrelationResult) error {
	f.stored++
	return nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func TestPipelineInvestigate(t *testing.T) {
	now := time.Now()
	metrics := make([]repo.MetricPoint, 0, 15)
	for i := 0; i < 15; i++ {
		value := 0.5
		if i > 10 {
			value = 2.5
		}
		metrics = append(metrics, repo.MetricPoint{Timestamp: now.Add(time.Duration(i) * time.Minute), Value: value})
	}

	logs := []repo.LogEntry{
		{Timestamp: now.Add(5 * time.Minute), Severity: "info", Count: 10},
		{Timestamp: now.Add(10 * time.Minute), Severity: "error", Count: 40},
	}

	traces := []repo.TraceSpan{
		{
			TraceID:   "trace-1",
			SpanID:    "span-1",
			Service:   "checkout",
			Operation: "HTTP POST",
			Duration:  900 * time.Millisecond,
			Status:    "error",
			Timestamp: now.Add(11 * time.Minute),
		},
	}

	fakeWeaviateClient := &fakeWeaviate{}

	pipeline := NewPipeline(
		nil,
		&fakeCoreClient{
			metrics: metrics,
			logs:    logs,
			traces:  traces,
			graph: []repo.ServiceGraphEdge{{
				Source:    "payments",
				Target:    "checkout",
				CallRate:  120,
				ErrorRate: 5,
			}},
		},
		fakeWeaviateClient,
		nil,
		NewCausalityEngine(nil),
		extractors.NewMetricExtractor(),
		extractors.NewLogsExtractor(),
		extractors.NewTracesExtractor(),
		nil, // llmClient
	)

	req := models.InvestigationRequest{
		IncidentID: "incident-123",
		Symptoms:   []string{"checkout"},
		TimeRange: models.TimeRange{
			Start: now,
			End:   now.Add(15 * time.Minute),
		},
		AffectedServices: []string{"checkout"},
		TenantID:         "tenant-a",
	}

	result, err := pipeline.Investigate(context.Background(), req)
	if err != nil {
		t.Fatalf("pipeline returned error: %v", err)
	}
	if result.RootCause == "" {
		t.Fatalf("expected root cause, got empty string")
	}
	if result.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", result.Confidence)
	}
	if len(result.RedAnchors) == 0 {
		t.Fatalf("expected red anchors, got none")
	}
	if len(result.Recommendations) == 0 {
		t.Fatalf("expected recommendations, got none")
	}
	if !sortIsChronological(result.Timeline) {
		t.Fatalf("timeline not chronological")
	}
	if fakeWeaviateClient.stored == 0 {
		t.Fatalf("expected correlation to be stored")
	}
	if !contains(result.AffectedServices, "payments") {
		t.Fatalf("expected topology neighbor to be included")
	}
	if !strings.Contains(result.RootCause, "payments") {
		t.Fatalf("expected causality-adjusted root cause, got %s", result.RootCause)
	}
}

func TestPipelineRulesFallback(t *testing.T) {
	now := time.Now()
	pipeline := NewPipeline(
		nil,
		&fakeCoreClient{metrics: []repo.MetricPoint{{Timestamp: now, Value: 3}}},
		nil,
		&RuleEngine{rules: []Rule{{
			ID:              "rule1",
			Match:           RuleMatch{Service: "checkout"},
			Recommendations: []string{"Rule Rec"},
		}}},
		NewCausalityEngine(nil),
		extractors.NewMetricExtractor(),
		extractors.NewLogsExtractor(),
		extractors.NewTracesExtractor(),
		nil, // llmClient
	)

	req := models.InvestigationRequest{
		TenantID:         "tenant",
		AffectedServices: []string{"checkout"},
		TimeRange:        models.TimeRange{Start: now, End: now.Add(time.Minute)},
	}

	result, err := pipeline.Investigate(context.Background(), req)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	if len(result.Recommendations) == 0 || result.Recommendations[0] != "Rule Rec" {
		t.Fatalf("expected rule-based recommendation")
	}
}

func sortIsChronological(events []models.TimelineEvent) bool {
	for i := 1; i < len(events); i++ {
		if events[i].Time.Before(events[i-1].Time) {
			return false
		}
	}
	return true
}

func TestPipelineLatencyWithinTarget(t *testing.T) {
	now := time.Now()
	metrics := []repo.MetricPoint{}
	for i := 0; i < 15; i++ {
		value := 0.5
		if i > 10 {
			value = 2.5
		}
		metrics = append(metrics, repo.MetricPoint{Timestamp: now.Add(time.Duration(i) * time.Minute), Value: value})
	}
	logs := []repo.LogEntry{
		{Timestamp: now.Add(5 * time.Minute), Severity: "info", Count: 10},
		{Timestamp: now.Add(10 * time.Minute), Severity: "error", Count: 40},
	}
	traces := []repo.TraceSpan{
		{
			TraceID:   "trace-1",
			SpanID:    "span-1",
			Service:   "checkout",
			Operation: "HTTP POST",
			Duration:  900 * time.Millisecond,
			Status:    "error",
			Timestamp: now.Add(11 * time.Minute),
		},
	}

	pipeline := NewPipeline(
		nil,
		&fakeCoreClient{metrics: metrics, logs: logs, traces: traces},
		&fakeWeaviate{},
		nil,
		NewCausalityEngine(nil),
		extractors.NewMetricExtractor(),
		extractors.NewLogsExtractor(),
		extractors.NewTracesExtractor(),
		nil, // llmClient
	)

	req := models.InvestigationRequest{
		IncidentID:       "incident-123",
		Symptoms:         []string{"checkout"},
		TimeRange:        models.TimeRange{Start: now, End: now.Add(15 * time.Minute)},
		AffectedServices: []string{"checkout"},
		TenantID:         "tenant-a",
	}

	start := time.Now()
	const runs = 50
	for i := 0; i < runs; i++ {
		if _, err := pipeline.Investigate(context.Background(), req); err != nil {
			t.Fatalf("pipeline returned error: %v", err)
		}
	}
	elapsed := time.Since(start)
	p95Estimate := (elapsed / runs) * 1
	if p95Estimate > 4*time.Second {
		t.Fatalf("p95 latency exceeds target: %v", p95Estimate)
	}
}
