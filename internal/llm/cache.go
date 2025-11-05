package llm

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type cacheItem struct {
	value   string
	expires time.Time
}

// LLMCache is a tiny in-memory TTL cache used to store prompt -> response mappings.
// It's not distributed; for production you can plug a Valkey/Redis implementation.
type LLMCache struct {
	mu    sync.RWMutex
	items map[string]cacheItem
	ttl   time.Duration
	hits  prometheus.Counter
	misses prometheus.Counter
}

// NewCache constructs a new in-memory cache with the provided TTL.
func NewCache(ttl time.Duration, registerer prometheus.Registerer) *LLMCache {
	hits := promauto.With(registerer).NewCounter(prometheus.CounterOpts{
		Name: "llm_cache_hits_total",
		Help: "Total number of LLM cache hits.",
	})
	misses := promauto.With(registerer).NewCounter(prometheus.CounterOpts{
		Name: "llm_cache_misses_total",
		Help: "Total number of LLM cache misses.",
	})

	return &LLMCache{items: make(map[string]cacheItem), ttl: ttl, hits: hits, misses: misses}
}

// Get returns the cached value and true if present and not expired.
func (c *LLMCache) Get(key string) (string, bool) {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().After(it.expires) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return "", false
	}
	return it.value, true
}

// Set stores a value in the cache with the configured TTL.
// Get returns the cached value and true if present and not expired.
func (c *LLMCache) Get(key string) (string, bool) {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		c.misses.Inc()
		return "", false
	}
	if time.Now().After(it.expires) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return "", false
	}
	c.hits.Inc()
	return it.value, true
}

// Set stores a value in the cache with the configured TTL.
func (c *LLMCache) Set(key, val string) {
	c.mu.Lock()
	c.items[key] = cacheItem{value: val, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}
