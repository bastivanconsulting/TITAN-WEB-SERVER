package cache

import (
	"net/http"
	"testing"
	"time"
)

func TestMemoryCache(t *testing.T) {
	c := NewMemoryCache()
	key := "test-key"
	value := []byte("hello world")
	headers := http.Header{"Content-Type": []string{"text/plain"}}
	ttl := 1 * time.Second

	// Test Set
	if err := c.Set(key, value, 200, headers, ttl); err != nil {
		t.Fatalf("Failed to set cache: %v", err)
	}

	// Test Get (Hit)
	entry, err := c.Get(key)
	if err != nil {
		t.Fatalf("Failed to get cache: %v", err)
	}
	if string(entry.Body) != string(value) {
		t.Errorf("Expected body %s, got %s", value, entry.Body)
	}
	if entry.StatusCode != 200 {
		t.Errorf("Expected StatusCode 200, got %d", entry.StatusCode)
	}
	if entry.Headers.Get("Content-Type") != "text/plain" {
		t.Errorf("Expected Content-Type text/plain, got %s", entry.Headers.Get("Content-Type"))
	}

	// Test Expiry
	time.Sleep(1100 * time.Millisecond)
	_, err = c.Get(key)
	if err != ErrExpired && err != ErrNotFound {
		t.Errorf("Expected cache to be expired or not found, got %v", err)
	}

	// Test Delete
	c.Set(key, value, 200, headers, ttl)
	c.Delete(key)
	_, err = c.Get(key)
	if err != ErrNotFound {
		t.Errorf("Expected cache to be not found after delete, got %v", err)
	}
}

func TestMemoryCacheEviction(t *testing.T) {
	c := NewMemoryCache()
	// Set small limit for testing (using reflection or just relying on constant?
	// Since we can't change the constant/struct field easily without exposing it,
	// we'll assume default 1000 and loop 1005 times.
	// Ideally, we should make maxSize configurable in NewMemoryCache or use a setter.)

	// Wait, we can't easily change the limit in test if it's hardcoded in struct field
	// and constructor uses constant.
	// But `maxSize` is unexported.
	// Let's modify NewMemoryCache to allow options or just update the test to use reflection?
	// No, better to update the code to make it testable.
	// But for now, let's just loop > 1000 times.

	limit := 1005
	headers := http.Header{}
	ttl := 1 * time.Minute

	for i := 0; i < limit; i++ {
		key := string(rune(i)) // simple key
		c.Set(key, []byte("data"), 200, headers, ttl)
	}

	// Count items
	// We can't access c.store directly (unexported).
	// But we can try to Get all keys.
	found := 0
	for i := 0; i < limit; i++ {
		key := string(rune(i))
		_, err := c.Get(key)
		if err == nil {
			found++
		}
	}

	if found > 1000 {
		t.Errorf("Expected max 1000 items, found %d", found)
	}
}
