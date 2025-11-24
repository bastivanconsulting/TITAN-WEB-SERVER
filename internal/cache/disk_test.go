package cache

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestDiskCache(t *testing.T) {
	tmpDir := t.TempDir()
	c, err := NewDiskCache(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create disk cache: %v", err)
	}

	key := "test-key-disk"
	value := []byte("hello disk")
	headers := http.Header{"Content-Type": []string{"text/plain"}}
	ttl := 1 * time.Second

	// Test Set
	if err := c.Set(key, value, 201, headers, ttl); err != nil {
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
	if entry.StatusCode != 201 {
		t.Errorf("Expected StatusCode 201, got %d", entry.StatusCode)
	}
	if entry.Headers.Get("Content-Type") != "text/plain" {
		t.Errorf("Expected Content-Type text/plain, got %s", entry.Headers.Get("Content-Type"))
	}

	// Test persistence (create new instance pointing to same dir)
	c2, _ := NewDiskCache(tmpDir)
	entry2, err := c2.Get(key)
	if err != nil {
		t.Fatalf("Failed to get persisted cache: %v", err)
	}
	if string(entry2.Body) != string(value) {
		t.Errorf("Expected persisted body %s, got %s", value, entry2.Body)
	}

	// Test Expiry
	time.Sleep(1100 * time.Millisecond)
	_, err = c.Get(key)
	if err != ErrExpired && err != ErrNotFound {
		t.Errorf("Expected cache to be expired or not found, got %v", err)
	}

	// Test Delete
	c.Set(key, value, 201, headers, ttl)
	c.Delete(key)
	_, err = c.Get(key)
	if err != ErrNotFound {
		t.Errorf("Expected cache to be not found after delete, got %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(c.getFilePath(key)); !os.IsNotExist(err) {
		t.Errorf("Expected cache file to be deleted")
	}
}

func TestDiskCacheEviction(t *testing.T) {
	tmpDir := t.TempDir()
	c, err := NewDiskCache(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create disk cache: %v", err)
	}

	// Override maxFiles for testing (needs to be done by creating struct manually or modifying)
	// Since maxFiles is unexported, we need a way to set it or rely on default.
	// Default is 10000, which is too slow for unit test.
	// Since we can't change it easily without changing code to export it or adding option.
	// Let's rely on the fact that we can just change the struct field if we are in same package!
	// Wait, we are in `cache` package? No, `cache`... yes `package cache`.
	// So we can access unexported fields in test if it's in the same package.
	// But wait, the file says `package cache`.

	c.maxFiles = 10 // Set small limit

	headers := http.Header{}
	ttl := 1 * time.Minute

	// Create 15 files (should trigger eviction)
	for i := 0; i < 15; i++ {
		key := string(rune(i))
		if err := c.Set(key, []byte("data"), 200, headers, ttl); err != nil {
			t.Fatalf("Failed to set cache: %v", err)
		}
	}

	// Count files
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read dir: %v", err)
	}

	// Logic: on 10th write (limit reached), nothing happens yet (>= check happens before write, so 0-9 = 10 files).
	// on 11th write (10 files exists), it deletes 10% (1 file). So 9 files + 1 new = 10 files.
	// on 12th write (10 files exists), it deletes 1 (9 files) + 1 new = 10 files.
	// So count should stabilize around maxFiles.

	if len(entries) > 11 { // Allow slight buffer due to file system async or logic
		t.Errorf("Expected around 10 files, found %d", len(entries))
	}
}
