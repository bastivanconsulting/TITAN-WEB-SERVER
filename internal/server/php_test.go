package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yookoala/gofast"
)

// MockClient implements gofast.Client
type MockClient struct {
	LastReq *gofast.Request
}

func (c *MockClient) Do(req *gofast.Request) (*gofast.ResponsePipe, error) {
	c.LastReq = req
	return &gofast.ResponsePipe{}, nil
}

func (c *MockClient) Close() error { return nil }

func TestNewPHPSessionHandler(t *testing.T) {
	tests := []struct {
		name         string
		root         string
		urlPath      string
		expectedPath string
		expectError  bool
	}{
		{
			name:         "Root request",
			root:         "/var/www",
			urlPath:      "/",
			expectedPath: "/var/www/index.php",
		},
		{
			name:         "File request",
			root:         "/var/www",
			urlPath:      "/test.php",
			expectedPath: "/var/www/test.php",
		},
		{
			name:         "Subdirectory file",
			root:         "/var/www",
			urlPath:      "/sub/test.php",
			expectedPath: "/var/www/sub/test.php",
		},
		{
			name:         "Directory request",
			root:         "/var/www",
			urlPath:      "/sub/",
			expectedPath: "/var/www/sub/index.php",
		},
		{
			name:         "Traversal attempt",
			root:         "/var/www",
			urlPath:      "/../../etc/passwd",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := NewPHPSessionHandler(tt.root)

			mockClient := &MockClient{}

			// Mock inner session handler
			inner := func(client gofast.Client, req *gofast.Request) (*gofast.ResponsePipe, error) {
				return client.Do(req)
			}

			// Wrap it
			handler := endpoint(inner)

			httpReq := httptest.NewRequest("GET", "http://example.com"+tt.urlPath, nil)
			req := gofast.NewRequest(httpReq)

			_, err := handler(mockClient, req)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if mockClient.LastReq == nil {
				t.Fatal("MockClient.Do was not called")
			}

			if mockClient.LastReq.Params["SCRIPT_FILENAME"] != tt.expectedPath {
				t.Errorf("Expected SCRIPT_FILENAME %s, got %s", tt.expectedPath, mockClient.LastReq.Params["SCRIPT_FILENAME"])
			}

			cleanRoot := filepath.Clean(tt.root)
			if mockClient.LastReq.Params["DOCUMENT_ROOT"] != cleanRoot {
				t.Errorf("Expected DOCUMENT_ROOT %s, got %s", cleanRoot, mockClient.LastReq.Params["DOCUMENT_ROOT"])
			}
		})
	}
}

func TestNewHybridHandler(t *testing.T) {
	// Create temporary directory for tests
	tmpDir, err := os.MkdirTemp("", "titan-test-root")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a static file
	staticFile := "style.css"
	staticContent := []byte("body { color: red; }")
	if err := os.WriteFile(filepath.Join(tmpDir, staticFile), staticContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Mock PHP Handler
	phpHit := false
	phpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		phpHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("PHP"))
	})

	handler := NewHybridHandler(tmpDir, phpHandler)

	// Test 1: Static file exists
	req := httptest.NewRequest("GET", "/"+staticFile, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for static file, got %d", w.Code)
	}
	if w.Body.String() != string(staticContent) {
		t.Errorf("Expected content %s, got %s", staticContent, w.Body.String())
	}
	if phpHit {
		t.Error("PHP handler should not be hit for static file")
	}

	// Test 2: PHP file (should go to PHP handler even if it exists, though usually PHP files shouldn't be served statically)
	// Create a dummy PHP file to simulate existence
	phpFile := "index.php"
	if err := os.WriteFile(filepath.Join(tmpDir, phpFile), []byte("<?php"), 0644); err != nil {
		t.Fatal(err)
	}

	phpHit = false
	req = httptest.NewRequest("GET", "/"+phpFile, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !phpHit {
		t.Error("Expected PHP handler to be hit for .php file")
	}

	// Test 3: File not found (should go to PHP handler)
	phpHit = false
	req = httptest.NewRequest("GET", "/unknown.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !phpHit {
		t.Error("Expected PHP handler to be hit for missing file")
	}

	// Test 4: Directory (should go to PHP handler, likely for index.php)
	// Note: root "/" is a directory
	phpHit = false
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !phpHit {
		t.Error("Expected PHP handler to be hit for directory")
	}
}
