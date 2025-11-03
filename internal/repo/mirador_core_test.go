package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestFetchServiceGraphCachesResults(t *testing.T) {
	hits := 0
	cacheStub := newStubCache()
	baseURL := "https://example.com"
	client := NewMiradorCoreClient(baseURL, "/metrics", "/logs", "/traces", "/api/v1/rca/service-graph", "/api/v1/unified/correlation", true, time.Second, cacheStub, time.Minute, time.Minute)
	client.httpClient = newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hits++
		if req.URL.Path != "/api/v1/rca/service-graph" {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		payload := map[string]any{
			"edges": []map[string]any{
				{"source": "checkout", "target": "payments", "call_rate": 42.0, "error_rate": 0.01},
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
		}, nil
	}))

	ctx := context.Background()
	start := time.Unix(1_700_000_000, 0)
	end := start.Add(5 * time.Minute)

	edges, err := client.FetchServiceGraph(ctx, "tenant-a", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected one upstream request, got %d", hits)
	}
	if len(edges) != 1 || edges[0].Source != "checkout" {
		t.Fatalf("unexpected response: %+v", edges)
	}

	cached, err := client.FetchServiceGraph(ctx, "tenant-a", start, end)
	if err != nil {
		t.Fatalf("unexpected cached error: %v", err)
	}
	if hits != 1 {
		t.Fatalf("cache miss triggered network call; hits=%d", hits)
	}
	if len(cached) != 1 || cached[0].Target != "payments" {
		t.Fatalf("unexpected cached payload: %+v", cached)
	}
}
