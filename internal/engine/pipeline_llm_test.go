package engine

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miradorstack/mirador-rca/internal/config"
	"github.com/miradorstack/mirador-rca/internal/llm"
	"github.com/miradorstack/mirador-rca/internal/models"
	"github.com/miradorstack/mirador-rca/internal/repo"
)

// fakeCore implements CoreClient returning empty signal sets — enough for a
// lightweight pipeline exercise where we only assert LLM invocation.
type fakeCore struct{}

func (f fakeCore) FetchMetricSeries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.MetricPoint, error) {
	return []repo.MetricPoint{}, nil
}
func (f fakeCore) FetchLogEntries(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.LogEntry, error) {
	return []repo.LogEntry{}, nil
}
func (f fakeCore) FetchTraceSpans(ctx context.Context, tenantID, service string, start, end time.Time) ([]repo.TraceSpan, error) {
	return []repo.TraceSpan{}, nil
}
func (f fakeCore) FetchServiceGraph(ctx context.Context, tenantID string, start, end time.Time) ([]repo.ServiceGraphEdge, error) {
	return []repo.ServiceGraphEdge{}, nil
}

func TestPipeline_LLMSummaryPopulation(t *testing.T) {
	// Start a test LLM HTTP server that returns a simple OpenAI-like JSON body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"This is the LLM summary"}}]}`))
	}))
	defer srv.Close()

	// Load default config and enable LLM, pointing to our test server.
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	cfg.LLM.Enabled = true
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.Timeout = 2 * time.Second
	config.SetRuntimeConfig(cfg)

	// Create pipeline with a fake core client and no external dependencies.
	p := NewPipeline(slog.Default(), fakeCore{}, nil, nil, nil, nil, nil, nil)

	// Attach a real LLM client pointing to the test server.
	lc := llm.NewClient(cfg.LLM, slog.Default())
	p.SetLLMClient(lc)

	// Build a minimal investigation request.
	req := models.InvestigationRequest{
		IncidentID:       "inc-1",
		Symptoms:         []string{"service-a"},
		TimeRange:        models.TimeRange{Start: time.Now().Add(-5 * time.Minute), End: time.Now()},
		AffectedServices: []string{"service-a"},
		AnomalyThreshold: 0.0,
		TenantID:         "t1",
	}

	ctx := context.Background()
	res, err := p.Investigate(ctx, req)
	if err != nil {
		t.Fatalf("investigate failed: %v", err)
	}

	if res.LLMSummary == "" {
		t.Fatalf("expected LLMSummary to be populated, got empty")
	}
	if res.LLMSummary != "This is the LLM summary" {
		t.Fatalf("unexpected LLMSummary: %q", res.LLMSummary)
	}
}
