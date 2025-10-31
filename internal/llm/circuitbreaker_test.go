package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miradorstack/mirador-rca/internal/config"
	"log/slog"
)

func TestCircuitBreaker_OpensOnFailures(t *testing.T) {
	// Server always returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()

	cfg := config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second, CircuitBreakerEnabled: true, CBFailureThreshold: 1, CBTimeout: 1 * time.Minute}
	client := NewClient(cfg, slog.Default())

	// first call should fail due to remote 500
	if _, err := client.Summarize(nil, "p"); err == nil {
		t.Fatalf("expected error on first failing call")
	}

	// second call should return circuit-open error quickly
	if _, err := client.Summarize(nil, "p"); err == nil {
		t.Fatalf("expected error from circuit breaker on subsequent call")
	}
}
