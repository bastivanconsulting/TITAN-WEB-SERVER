package server

import (
	"net/http/httptest"
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
		})
	}
}
