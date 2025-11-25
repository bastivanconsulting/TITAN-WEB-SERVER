package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yookoala/gofast"
)

// NewPHPSessionHandler returns a Middleware that maps the request URL to a file in the given root.
// It ensures the resulting path is within the root directory.
// If the path ends in a slash, it appends "index.php".
func NewPHPSessionHandler(root string) func(gofast.SessionHandler) gofast.SessionHandler {
	return func(inner gofast.SessionHandler) gofast.SessionHandler {
		return func(client gofast.Client, req *gofast.Request) (*gofast.ResponsePipe, error) {
			// Wrap the client to intercept the Do call
			proxyClient := &paramModifierClient{
				Client:   client,
				modifier: func(r *gofast.Request) error {
					// Access the raw HTTP request
					rawReq := r.Raw
					if rawReq == nil {
						return fmt.Errorf("raw request is nil")
					}

					// Clean the root path
					cleanRoot := filepath.Clean(root)

					// Construct the full path
					fullPath := filepath.Join(cleanRoot, rawReq.URL.Path)

					// Security check
					if fullPath != cleanRoot && !strings.HasPrefix(fullPath, cleanRoot+string(filepath.Separator)) {
						return fmt.Errorf("access denied: path traversal attempt")
					}

					// Append index.php if directory
					if strings.HasSuffix(rawReq.URL.Path, "/") {
						fullPath = filepath.Join(fullPath, "index.php")
					}

					// Update Params
					r.Params["SCRIPT_FILENAME"] = fullPath
					r.Params["DOCUMENT_ROOT"] = cleanRoot
					return nil
				},
			}

			return inner(proxyClient, req)
		}
	}
}

// paramModifierClient wraps gofast.Client to modify request params before sending
type paramModifierClient struct {
	gofast.Client
	modifier func(*gofast.Request) error
}

func (c *paramModifierClient) Do(req *gofast.Request) (*gofast.ResponsePipe, error) {
	if err := c.modifier(req); err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

// NewHybridHandler returns a handler that serves static files if they exist,
// otherwise passes the request to the provided PHP handler.
func NewHybridHandler(root string, phpHandler http.Handler) http.Handler {
	fileServer := http.FileServer(http.Dir(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		fullPath := filepath.Join(root, path)

		// Security check (redundant but safe)
		cleanRoot := filepath.Clean(root)
		if fullPath != cleanRoot && !strings.HasPrefix(fullPath, cleanRoot+string(filepath.Separator)) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Check if file exists and is not a directory
		info, err := os.Stat(fullPath)
		if err == nil && !info.IsDir() {
			// If it's a PHP file, pass to PHP handler
			if strings.HasSuffix(strings.ToLower(path), ".php") {
				phpHandler.ServeHTTP(w, r)
				return
			}

			// Otherwise serve as static file
			// Strip prefix if necessary? FileServer serves relative to its root.
			// r.URL.Path is used by FileServer.
			// Since we created FileServer(Dir(root)), and r.URL.Path is /foo.css, it looks for root/foo.css.
			// This matches our fullPath calculation.
			fileServer.ServeHTTP(w, r)
			return
		}

		// If directory or file not found, pass to PHP handler (which handles index.php or 404 via PHP script)
		phpHandler.ServeHTTP(w, r)
	})
}
