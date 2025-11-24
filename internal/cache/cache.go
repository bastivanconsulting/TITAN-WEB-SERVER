package cache

import (
	"errors"
	"net/http"
	"time"
)

// Entry represents a cached HTTP response
type Entry struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
	Expiry     time.Time
}

// Cache is the interface for caching mechanisms
type Cache interface {
	Get(key string) (*Entry, error)
	Set(key string, value []byte, statusCode int, headers http.Header, ttl time.Duration) error
	Delete(key string) error
}

var ErrNotFound = errors.New("cache entry not found")
var ErrExpired = errors.New("cache entry expired")
