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

func TestNewPHPEndpoint(t *testing.T) {
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
			expectedPath: filepath.Join("/var/www", "index.php"),
		},
		{
			name:         "File request",
			root:         "/var/www",
			urlPath:      "/test.php",
			expectedPath: filepath.Join("/var/www", "test.php"),
		},
		{
			name:         "Subdirectory file",
			root:         "/var/www",
			urlPath:      "/sub/test.php",
			expectedPath: filepath.Join("/var/www", "sub", "test.php"),
		},
		{
			name:         "Directory request",
			root:         "/var/www",
			urlPath:      "/sub/",
			expectedPath: filepath.Join("/var/www", "sub", "index.php"),
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
			endpoint := NewPHPEndpoint(tt.root)

			mockClient := &MockClient{}

			// Mock inner session handler
			// The inner handler calls client.Do(req)
			inner := func(client gofast.Client, req *gofast.Request) (*gofast.ResponsePipe, error) {
				return client.Do(req)
			}

			// Wrap it
			handler := endpoint(inner)

			httpReq := httptest.NewRequest("GET", "http://example.com"+tt.urlPath, nil)
			httpReq.RemoteAddr = "192.0.2.1:12345"
			req := gofast.NewRequest(httpReq)

			// Initialize params because gofast.NewRequest might not do it?
			// gofast.NewRequest creates a request with empty Params map.

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

			// Check the request passed to the client
			// The client is wrapped, so the mockClient.LastReq should have the modified params
			// because the wrapper calls modifier() then client.Do().

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

			// Verify other params
			if mockClient.LastReq.Params["REQUEST_METHOD"] != "GET" {
				t.Errorf("Expected REQUEST_METHOD GET, got %s", mockClient.LastReq.Params["REQUEST_METHOD"])
			}
			if mockClient.LastReq.Params["REMOTE_ADDR"] != "192.0.2.1" {
				t.Errorf("Expected REMOTE_ADDR 192.0.2.1, got %s", mockClient.LastReq.Params["REMOTE_ADDR"])
			}
			if mockClient.LastReq.Params["REMOTE_PORT"] != "12345" {
				t.Errorf("Expected REMOTE_PORT 12345, got %s", mockClient.LastReq.Params["REMOTE_PORT"])
			}
			if mockClient.LastReq.Params["SERVER_NAME"] != "example.com" {
				t.Errorf("Expected SERVER_NAME example.com, got %s", mockClient.LastReq.Params["SERVER_NAME"])
			}
		})
	}
}

// MockHandler is a simple http.Handler that records if it was called
type MockHandler struct {
	Called bool
}

func (m *MockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.Called = true
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("PHP Executed"))
}

func TestHybridHandlerReal(t *testing.T) {
	// Create a temporary directory for our web root
	tmpDir, err := os.MkdirTemp("", "titan-test-www")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a static file
	// Note: http.FileServer redirects /index.html to /, so we test with style.css to verify static serving
	if err := os.WriteFile(filepath.Join(tmpDir, "style.css"), []byte("body {}"), 0644); err != nil {
		t.Fatalf("Failed to write style.css: %v", err)
	}

	// Create a PHP file
	if err := os.WriteFile(filepath.Join(tmpDir, "index.php"), []byte("<?php echo 'hi';"), 0644); err != nil {
		t.Fatalf("Failed to write index.php: %v", err)
	}

	// Mock PHP Handler
	mockPHP := &MockHandler{}

	// Initialize the Hybrid Handler
	handler := NewHybridHandler(tmpDir, mockPHP)

	tests := []struct {
		name           string
		path           string
		expectPHP      bool
		expectContent  string // For static files
		expectedStatus int
	}{
		{
			name:           "Static CSS file",
			path:           "/style.css",
			expectPHP:      false,
			expectContent:  "body {}",
			expectedStatus: 200,
		},
		{
			name:           "Explicit PHP file",
			path:           "/index.php",
			expectPHP:      true,
			expectedStatus: 200,
		},
		{
			name:      "Directory with index.php (should prefer PHP)",
			path:      "/",
			expectPHP: true,
			expectedStatus: 200,
		},
		{
			name:           "Missing PHP file (should 404)",
			path:           "/missing.php",
			expectPHP:      false,
			expectContent:  "404 page not found\n",
			expectedStatus: 404,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockPHP.Called = false
			req := httptest.NewRequest("GET", "http://example.com"+tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if tt.expectPHP {
				if !mockPHP.Called {
					t.Errorf("Expected request to be handled by PHP, but it wasn't")
				}
			} else {
				if mockPHP.Called {
					t.Errorf("Expected request to be handled by Static handler, but PHP handler was called")
				}
				if tt.expectContent != "" && w.Body.String() != tt.expectContent {
					t.Errorf("Expected content %q, got %q", tt.expectContent, w.Body.String())
				}
			}

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestHybridHandlerStaticFallback(t *testing.T) {
	// Test case where index.php does NOT exist, but index.html does
	tmpDir, err := os.MkdirTemp("", "titan-test-www-static")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	htmlContent := "<h1>Only HTML</h1>"
	if err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(htmlContent), 0644); err != nil {
		t.Fatalf("Failed to write index.html: %v", err)
	}

	mockPHP := &MockHandler{}
	handler := NewHybridHandler(tmpDir, mockPHP)

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if mockPHP.Called {
		t.Errorf("Expected request to be handled by Static handler (index.html), but PHP handler was called")
	}

	// http.FileServer serves index.html for directory requests
	// Note: It might do a redirect if path doesn't end in slash, but here path is "/"
	if w.Body.String() != htmlContent {
		t.Errorf("Expected content %q, got %q", htmlContent, w.Body.String())
	}
}
