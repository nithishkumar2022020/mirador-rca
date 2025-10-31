package llm

import (
	"sync"
	"time"
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
}

// NewCache constructs a new in-memory cache with the provided TTL.
func NewCache(ttl time.Duration) *LLMCache {
	return &LLMCache{items: make(map[string]cacheItem), ttl: ttl}
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
func (c *LLMCache) Set(key, val string) {
	c.mu.Lock()
	c.items[key] = cacheItem{value: val, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}
