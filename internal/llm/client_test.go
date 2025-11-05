package llm

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "log/slog"
)

func TestGenerate_Success(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/generate" {
            t.Fatalf("unexpected path: %s", r.URL.Path)
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"text":"This is a summary.","usage":{"prompt_tokens":1,"completion_tokens":3,"total_tokens":4},"finish_reason":"stop"}`))
    }))
    defer srv.Close()

    c := NewVLLMClient(VLLMConfig{BaseURL: srv.URL, Timeout: 2 * time.Second}, slog.Default())

    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    resp, err := c.Generate(ctx, "please summarize", GenerateOptions{MaxTokens: 10})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if resp.Text != "This is a summary." {
        t.Fatalf("unexpected text: %q", resp.Text)
    }
    if resp.Usage.TotalTokens != 4 {
        t.Fatalf("unexpected tokens: %+v", resp.Usage)
    }
}

func TestGenerate_Non2xx(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "bad", http.StatusInternalServerError)
    }))
    defer srv.Close()

    c := NewVLLMClient(VLLMConfig{BaseURL: srv.URL, Timeout: 1 * time.Second}, slog.Default())

    ctx := context.Background()
    if _, err := c.Generate(ctx, "x", GenerateOptions{}); err == nil {
        t.Fatalf("expected error for non-2xx response")
    }
}

func TestGenerate_Timeout(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        time.Sleep(200 * time.Millisecond)
        _, _ = w.Write([]byte(`{"text":"ok","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"finish_reason":"stop"}`))
    }))
    defer srv.Close()

    c := NewVLLMClient(VLLMConfig{BaseURL: srv.URL, Timeout: 10 * time.Millisecond}, slog.Default())
    ctx := context.Background()
    if _, err := c.Generate(ctx, "x", GenerateOptions{}); err == nil {
        t.Fatalf("expected timeout error")
    }
}

func TestHealth_Serves200(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/health" {
            t.Fatalf("unexpected path: %s", r.URL.Path)
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer srv.Close()

    c := NewVLLMClient(VLLMConfig{BaseURL: srv.URL, Timeout: 1 * time.Second}, slog.Default())
    if err := c.Health(context.Background()); err != nil {
        t.Fatalf("unexpected health error: %v", err)
    }
}
