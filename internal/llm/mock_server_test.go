//go:build test

package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

type mockServer struct {
	server *httptest.Server
	mu     sync.Mutex
	config struct {
		baseURL string
		timeout time.Duration
	}
}

// HealthResponse represents the health check response
// This is a simplified version of the actual vLLM health check response
type HealthResponse struct {
	Status string `json:"status"`
}

// CompletionRequest represents the completion request
type CompletionRequest struct {
	Prompt string `json:"prompt"`
}

// CompletionResponse represents the completion response
type CompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Text  string `json:"text"`
		Index int    `json:"index"`
	} `json:"choices"`
}

// newMockServer creates a new mock vLLM server for testing
func newMockServer() *mockServer {
	m := &mockServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", m.handleHealth)
	mux.HandleFunc("/generate", m.handleGenerate)

	m.server = httptest.NewServer(mux)
	m.config.baseURL = m.server.URL
	m.config.timeout = 30 * time.Second

	return m
}

// URL returns the base URL of the mock server
func (m *mockServer) URL() string {
	return m.server.URL
}

// Close shuts down the mock server
func (m *mockServer) Close() {
	m.server.Close()
}

// handleHealth handles health check requests
func (m *mockServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(HealthResponse{
		Status: "ok",
	})
}

// handleGenerate handles generate requests
func (m *mockServer) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Return a simple response that matches what the client expects
	response := struct {
		Text     string `json:"text"`
		Usage    struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Latency time.Duration `json:"latency"`
	}{
		Text: "This is a mock response to: " + req.Prompt,
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     len(req.Prompt) / 4, // Rough estimate
			CompletionTokens: 10,
			TotalTokens:      len(req.Prompt)/4 + 10,
		},
		Latency: 100 * time.Millisecond,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
