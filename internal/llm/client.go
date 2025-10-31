package llm

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/miradorstack/mirador-rca/internal/config"
	"github.com/sony/gobreaker"
	"log/slog"
)

// LLMClient defines the behaviour used by the pipeline.
type LLMClient interface {
	// Summarize accepts a prompt and returns a short textual summary.
	Summarize(ctx context.Context, prompt string) (string, error)
}

// Client is a minimal HTTP LLM client. It speaks a simple chat/completion JSON shape
// that many vLLM / OpenAI-compatible servers accept. It purposefully keeps
// dependencies small and uses the stdlib HTTP client so it works in air-gapped envs.
type Client struct {
	endpoint string
	apiKey   string
	client   *resty.Client
	logger   *slog.Logger
	cache    *LLMCache
	cb       *gobreaker.CircuitBreaker
}

// NewClient constructs a new Client using values from the runtime configuration.
func NewClient(cfg config.LLMConfig, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	to := cfg.Timeout
	if to <= 0 {
		to = 5 * time.Second
	}
	rc := resty.New()
	rc.SetTimeout(to)
	if cfg.APIKey != "" {
		rc.SetAuthToken(cfg.APIKey)
	}
	// Add a small retry to handle transient errors; retries are conservative to
	// avoid long tail latency. These are safe in air-gapped setups since they
	// happen locally against the configured endpoint.
	rc.SetRetryCount(1)
	rc.SetRetryWaitTime(100 * time.Millisecond)
	c := &Client{endpoint: cfg.BaseURL, apiKey: cfg.APIKey, client: rc, logger: logger}

	// optional cache
	if cfg.CacheEnabled {
		ttl := cfg.CacheTTL
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		c.cache = NewCache(ttl)
	}

	// optional circuit breaker
	if cfg.CircuitBreakerEnabled {
		st := gobreaker.Settings{
			Name:        "llm-client",
			MaxRequests: 1,
			Interval:    0,
			Timeout:     cfg.CBTimeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= cfg.CBFailureThreshold
			},
		}
		c.cb = gobreaker.NewCircuitBreaker(st)
	}

	return c
}

// chatRequest is the request payload sent to the LLM endpoint.
type chatRequest struct {
	Model       string              `json:"model,omitempty"`
	Messages    []map[string]string `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature,omitempty"`
}

// chatResponse is a lightweight shape covering common OpenAI-like responses.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"` // some servers use choices[].text
	} `json:"choices"`
}

// Summarize sends the prompt to the configured LLM endpoint and returns the textual reply.
func (c *Client) Summarize(ctx context.Context, prompt string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("llm client not configured")
	}
	// Build payload
	payload := chatRequest{
		Model:     "gpt-like",
		Messages:  []map[string]string{{"role": "user", "content": prompt}},
		MaxTokens: 200,
	}

	// compute cache key and check cache first
	key := prompt
	if c.cache != nil {
		// include a tiny fingerprint of model/params
		h := sha1.New()
		h.Write([]byte(prompt))
		h.Write([]byte("|"))
		h.Write([]byte("model:"))
		h.Write([]byte(""))
		k := hex.EncodeToString(h.Sum(nil))
		if v, ok := c.cache.Get(k); ok {
			requestsTotal.WithLabelValues("cache_hit").Inc()
			return v, nil
		}
		key = k
	}

	// make request (possibly through circuit breaker)
	doRequest := func() (interface{}, error) {
		start := time.Now()
		requestsTotal.WithLabelValues("attempt").Inc()
		resp, err := c.client.R().SetContext(ctx).SetHeader("Content-Type", "application/json").SetBody(payload).Post(c.endpoint)
		latency := time.Since(start).Seconds()
		requestLatency.Observe(latency)
		if err != nil {
			requestsTotal.WithLabelValues("error").Inc()
			c.logger.Warn("llm request failed", slog.Any("error", err))
			return nil, err
		}
		body := resp.Body()
		if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
			requestsTotal.WithLabelValues("error").Inc()
			c.logger.Warn("llm endpoint returned non-2xx", slog.Int("status", resp.StatusCode()), slog.String("body", string(body)))
			return nil, fmt.Errorf("llm returned status %d", resp.StatusCode())
		}
		requestsTotal.WithLabelValues("success").Inc()
		return body, nil
	}

	var bodyBytes []byte
	if c.cb != nil {
		res, err := c.cb.Execute(func() (interface{}, error) {
			return doRequest()
		})
		if err != nil {
			c.logger.Warn("llm request failed (circuit)", slog.Any("error", err))
			return "", err
		}
		if b, ok := res.([]byte); ok {
			bodyBytes = b
		} else {
			// attempt to coerce
			if s, ok := res.(string); ok {
				bodyBytes = []byte(s)
			}
		}
	} else {
		res, err := doRequest()
		if err != nil {
			return "", err
		}
		if b, ok := res.([]byte); ok {
			bodyBytes = b
		}
	}

	var cr chatResponse
	if err := json.Unmarshal(bodyBytes, &cr); err != nil {
		// if unmarshal fails, return raw body as fallback
		c.logger.Debug("failed to parse llm response, returning raw body", slog.Any("error", err))
		return string(bodyBytes), nil
	}

	if len(cr.Choices) > 0 {
		// prefer message.content if present, otherwise choices[].text
		if cr.Choices[0].Message.Content != "" {
			if c.cache != nil {
				c.cache.Set(key, cr.Choices[0].Message.Content)
			}
			return cr.Choices[0].Message.Content, nil
		}
		if cr.Choices[0].Text != "" {
			if c.cache != nil {
				c.cache.Set(key, cr.Choices[0].Text)
			}
			return cr.Choices[0].Text, nil
		}
	}
	// empty choice -> return empty string
	return "", nil
}
