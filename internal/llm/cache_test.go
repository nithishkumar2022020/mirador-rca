package llm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miradorstack/mirador-rca/internal/config"
	"log/slog"
)

func TestCache_HitsAvoidRemoteCall(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"cached response"}}]}`))
	}))
	defer srv.Close()

	cfg := config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second, CacheEnabled: true, CacheTTL: 1 * time.Minute}
	client := NewClient(cfg, slog.Default())

	// first call should hit remote
	s, err := client.Summarize(nil, "prompt-1")
	if err != nil {
		t.Fatalf("first summarize failed: %v", err)
	}
	if s != "cached response" {
		t.Fatalf("unexpected response: %q", s)
	}

	// second call should be served from cache and not increase remote call count
	s2, err := client.Summarize(nil, "prompt-1")
	if err != nil {
		t.Fatalf("second summarize failed: %v", err)
	}
	if s2 != s {
		t.Fatalf("cached response mismatch: %q vs %q", s2, s)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected remote called once, got %d", calls)
	}
}
