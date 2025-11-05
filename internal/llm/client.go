package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
    "sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Client represents an LLM client that can use LMCache for caching
type Client interface {
	Generate(ctx context.Context, prompt string, options GenerateOptions) (*GenerationResponse, error)
	Health(ctx context.Context) error
	Close() error
}

// GenerateOptions contains options for LLM generation
type GenerateOptions struct {
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	TopK        int     `json:"top_k,omitempty"`
	Stream      bool    `json:"stream,omitempty"`
}

// GenerationResponse represents the response from LLM generation
type GenerationResponse struct {
	Text         string        `json:"text"`
	Usage        TokenUsage    `json:"usage"`
	FinishReason string        `json:"finish_reason"`
	Latency      time.Duration `json:"-"`
}

// TokenUsage represents token usage statistics
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// VLLMClient implements Client for vLLM with LMCache integration
type VLLMClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger

    // simple circuit breaker
    mu                  sync.Mutex
    cbFailures          int
    cbFailureThreshold  int
    cbOpenUntil         time.Time
    cbResetTimeout      time.Duration
    circuitBreakerState prometheus.Gauge

    // optional in-memory TTL cache for prompt -> response
    cache *LLMCache
}

// VLLMConfig holds configuration for vLLM client
type VLLMConfig struct {
	BaseURL string
	Timeout time.Duration
}

// NewVLLMClient creates a new vLLM client
func NewVLLMClient(config VLLMConfig, logger *slog.Logger, registerer prometheus.Registerer) *VLLMClient {
	if logger == nil {
		logger = slog.Default()
	}

    return &VLLMClient{
		baseURL: config.BaseURL,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
        logger: logger,
        cbFailureThreshold: 3,
        cbResetTimeout: 30 * time.Second,
		circuitBreakerState: promauto.With(registerer).NewGauge(prometheus.GaugeOpts{
			Name: "llm_circuit_breaker_state",
			Help: "Current state of the LLM circuit breaker (0=closed, 1=open).",
		}),
        cache: NewCache(1 * time.Minute),
	}
}

// Generate sends a generation request to the vLLM server
func (c *VLLMClient) Generate(ctx context.Context, prompt string, options GenerateOptions) (*GenerationResponse, error) {
    start := time.Now()

    // circuit breaker: if open, fast-fail
    c.mu.Lock()
    if time.Now().Before(c.cbOpenUntil) {
        c.mu.Unlock()
        requestsTotal.WithLabelValues("error").Inc()
        return nil, fmt.Errorf("llm circuit open; retry after %s", time.Until(c.cbOpenUntil).Truncate(time.Second))
    }
    c.mu.Unlock()

    // cache lookup (only for non-streaming)
    if !options.Stream && c.cache != nil {
        if val, ok := c.cache.Get(prompt); ok {
            resp := &GenerationResponse{Text: val, Usage: TokenUsage{}, FinishReason: "cache", Latency: time.Since(start)}
            requestsTotal.WithLabelValues("success").Inc()
            requestLatency.Observe(resp.Latency.Seconds())
            return resp, nil
        }
    }

	request := map[string]interface{}{
		"prompt": prompt,
		"stream": options.Stream,
	}

	// Add optional parameters
	if options.MaxTokens > 0 {
		request["max_tokens"] = options.MaxTokens
	}
	if options.Temperature > 0 {
		request["temperature"] = options.Temperature
	}
	if options.TopP > 0 {
		request["top_p"] = options.TopP
	}
	if options.TopK > 0 {
		request["top_k"] = options.TopK
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(req)
	if err != nil {
        requestsTotal.WithLabelValues("error").Inc()
        c.recordFailure()
        return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
        requestsTotal.WithLabelValues("error").Inc()
        c.recordFailure()
        return nil, fmt.Errorf("vLLM request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var vllmResponse struct {
		Text         string     `json:"text"`
		Usage        TokenUsage `json:"usage"`
		FinishReason string     `json:"finish_reason"`
	}

    if err := json.NewDecoder(resp.Body).Decode(&vllmResponse); err != nil {
        requestsTotal.WithLabelValues("error").Inc()
        c.recordFailure()
        return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	response := &GenerationResponse{
		Text:         vllmResponse.Text,
		Usage:        vllmResponse.Usage,
		FinishReason: vllmResponse.FinishReason,
		Latency:      time.Since(start),
	}

    c.logger.Debug("LLM generation completed",
		slog.Duration("latency", response.Latency),
		slog.Int("prompt_tokens", response.Usage.PromptTokens),
		slog.Int("completion_tokens", response.Usage.CompletionTokens))

    // Metrics
    requestsTotal.WithLabelValues("success").Inc()
    requestLatency.Observe(response.Latency.Seconds())

    // reset failures on success
    c.recordSuccess()

    // store in cache
    if !options.Stream && c.cache != nil {
        c.cache.Set(prompt, response.Text)
    }

	return response, nil
}

// Health checks the health of the vLLM server
func (c *VLLMClient) Health(ctx context.Context) error {
    start := time.Now()
    req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health request: %w", err)
	}

    resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vLLM health check failed with status %d", resp.StatusCode)
	}

    // Record latency as success for health calls as well (optional)
    requestLatency.Observe(time.Since(start).Seconds())
	return nil
}

// Close closes the HTTP client
func (c *VLLMClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// recordFailure increments failure count and opens circuit if threshold reached
func (c *VLLMClient) recordFailure() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.cbFailures++
    if c.cbFailures >= c.cbFailureThreshold {
        c.cbOpenUntil = time.Now().Add(c.cbResetTimeout)
        c.logger.Warn("LLM circuit opened", slog.Duration("reset_after", c.cbResetTimeout))
        c.circuitBreakerState.Set(1)
    }
}

// recordSuccess resets failure counters and closes circuit
func (c *VLLMClient) recordSuccess() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.cbFailures = 0
    c.cbOpenUntil = time.Time{}
    c.circuitBreakerState.Set(0)
}
