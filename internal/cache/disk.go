package cache

import (
	"encoding/gob"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DiskCache implements a file-based cache
type DiskCache struct {
	basePath string
	maxFiles int
}

const DefaultMaxDiskFiles = 10000

// NewDiskCache creates a new DiskCache
func NewDiskCache(path string) (*DiskCache, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}
	return &DiskCache{
		basePath: path,
		maxFiles: DefaultMaxDiskFiles,
	}, nil
}

func (c *DiskCache) getFilePath(key string) string {
	// Simple hashing or just escaping might be needed. For now assuming key is safe-ish.
	// In production, SHA256 the key to avoid filesystem issues.
	// But let's simple encode for now.
	return filepath.Join(c.basePath, fmt.Sprintf("%x.cache", key))
}

// Get retrieves an entry from the disk
func (c *DiskCache) Get(key string) (*Entry, error) {
	// No global lock for Get (OS handles concurrent reads mostly fine)
	path := c.getFilePath(key)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entry Entry
	decoder := gob.NewDecoder(file)
	if err := decoder.Decode(&entry); err != nil {
		return nil, fmt.Errorf("failed to decode cache entry: %w", err)
	}

	if time.Now().After(entry.Expiry) {
		_ = os.Remove(path) // Lazy cleanup
		return nil, ErrExpired
	}

	return &entry, nil
}

// Set saves an entry to the disk
func (c *DiskCache) Set(key string, value []byte, statusCode int, headers http.Header, ttl time.Duration) error {
	// Simple eviction check
	// NOTE: Checking file count on every write is expensive (readdir).
	// For production, we should track count in memory or do this probabilistically/periodically.
	// For this implementation, we'll do a probabilistic check (e.g. 1 in 100) or just ignore for MVP performance
	// unless specified. But to satisfy "safety" requirement, let's just do a naive check if we assume
	// low write frequency or just prune if we hit an error.

	// Better approach for "safety" without killing performance:
	// Only check when we think we might be full? No way to know.
	// Let's implement a "Prune" method and call it here. To avoid listing all files every time,
	// maybe we can skip it or accept the cost for now as "safety > performance" for this specific feedback.

	// Implementation: Naive cleanup (delete arbitrary files if too many)
	// Optimization: We could use `filepath.WalkDir` but `ReadDir` is simpler for flat dir.
	entries, err := os.ReadDir(c.basePath)
	if err == nil && len(entries) >= c.maxFiles {
		// Delete 10% of files to make space
		toDelete := len(entries) / 10
		if toDelete == 0 { toDelete = 1 }
		for i := 0; i < toDelete; i++ {
			_ = os.Remove(filepath.Join(c.basePath, entries[i].Name()))
		}
	}

	// Atomic write: write to temp file then rename
	// This avoids locking readers while writing
	entry := Entry{
		StatusCode: statusCode,
		Body:       value,
		Headers:    headers.Clone(),
		Expiry:     time.Now().Add(ttl),
	}

	tmpFile, err := os.CreateTemp(c.basePath, "tmp-cache-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name()) // Clean up on error

	encoder := gob.NewEncoder(tmpFile)
	if err := encoder.Encode(entry); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to encode cache entry: %w", err)
	}
	tmpFile.Close()

	path := c.getFilePath(key)
	if err := os.Rename(tmpFile.Name(), path); err != nil {
		return fmt.Errorf("failed to move cache file to place: %w", err)
	}

	return nil
}

// Delete removes an entry from the disk
func (c *DiskCache) Delete(key string) error {
	path := c.getFilePath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
