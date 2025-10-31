package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miradorstack/mirador-rca/internal/config"
	"log/slog"
)

func TestSummarize_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"This is a summary."}}]}`))
	}))
	defer srv.Close()

	cfg := config.LLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second}
	c := NewClient(cfg, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := c.Summarize(ctx, "please summarize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "This is a summary." {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestSummarize_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.LLMConfig{BaseURL: srv.URL, Timeout: 1 * time.Second}
	c := NewClient(cfg, slog.Default())

	ctx := context.Background()
	_, err := c.Summarize(ctx, "x")
	if err == nil {
		t.Fatalf("expected error for non-2xx response")
	}
}

func TestSummarize_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"text":"ok"}]}`))
	}))
	defer srv.Close()

	cfg := config.LLMConfig{BaseURL: srv.URL, Timeout: 0}
	c := NewClient(cfg, slog.Default())
	// override client timeout to very small for deterministic test
	c.client.SetTimeout(10 * time.Millisecond)

	ctx := context.Background()
	_, err := c.Summarize(ctx, "x")
	if err == nil {
		t.Fatalf("expected timeout error")
	}
}
