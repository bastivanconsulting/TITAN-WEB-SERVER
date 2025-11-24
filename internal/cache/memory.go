package cache

import (
	"net/http"
	"sync"
	"time"
)

// MemoryCache implements an in-memory cache using a map with a simple eviction policy
type MemoryCache struct {
	mu      sync.RWMutex
	store   map[string]Entry
	maxSize int
}

const DefaultMaxItems = 1000

// NewMemoryCache creates a new MemoryCache
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		store:   make(map[string]Entry),
		maxSize: DefaultMaxItems,
	}
}

// Get retrieves an entry from the cache
func (c *MemoryCache) Get(key string) (*Entry, error) {
	c.mu.RLock()
	entry, ok := c.store[key]
	c.mu.RUnlock()

	if !ok {
		return nil, ErrNotFound
	}

	if time.Now().After(entry.Expiry) {
		c.Delete(key) // Lazy cleanup
		return nil, ErrExpired
	}

	return &entry, nil
}

// Set adds or updates an entry in the cache
func (c *MemoryCache) Set(key string, value []byte, statusCode int, headers http.Header, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction: if over limit, just remove random items (iteration order is random)
	if len(c.store) >= c.maxSize {
		// Try to delete one item
		for k := range c.store {
			delete(c.store, k)
			break
		}
	}

	c.store[key] = Entry{
		StatusCode: statusCode,
		Body:       value,
		Headers:    headers.Clone(), // Clone to avoid race conditions if headers are modified elsewhere
		Expiry:     time.Now().Add(ttl),
	}
	return nil
}

// Delete removes an entry from the cache
func (c *MemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, key)
	return nil
}
