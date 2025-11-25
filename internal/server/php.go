package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yookoala/gofast"
)

// NewPHPEndpoint returns a Middleware that maps the request URL to a file in the given root.
// It ensures the resulting path is within the root directory.
// If the path ends in a slash, it appends "index.php".
func NewPHPEndpoint(root string) func(gofast.SessionHandler) gofast.SessionHandler {
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
					// Standard CGI variables
					r.Params["SCRIPT_FILENAME"] = fullPath
					r.Params["DOCUMENT_ROOT"] = cleanRoot
					r.Params["REQUEST_URI"] = rawReq.URL.RequestURI()
					r.Params["DOCUMENT_URI"] = rawReq.URL.Path // or rawReq.URL.RequestURI()? Path is safer.
					r.Params["QUERY_STRING"] = rawReq.URL.RawQuery
					r.Params["REQUEST_METHOD"] = rawReq.Method
					r.Params["CONTENT_TYPE"] = rawReq.Header.Get("Content-Type")
					r.Params["CONTENT_LENGTH"] = fmt.Sprintf("%d", rawReq.ContentLength)
					r.Params["SERVER_PROTOCOL"] = rawReq.Proto
					r.Params["GATEWAY_INTERFACE"] = "CGI/1.1"
					r.Params["SERVER_SOFTWARE"] = "Titan Web Server"

					// Parse Remote Addr/Port
					if host, port, err := net.SplitHostPort(rawReq.RemoteAddr); err == nil {
						r.Params["REMOTE_ADDR"] = host
						r.Params["REMOTE_PORT"] = port
					} else {
						r.Params["REMOTE_ADDR"] = rawReq.RemoteAddr
					}

					// Parse Server Addr/Port
					if host, port, err := net.SplitHostPort(rawReq.Host); err == nil {
						r.Params["SERVER_NAME"] = host
						r.Params["SERVER_PORT"] = port
					} else {
						r.Params["SERVER_NAME"] = rawReq.Host
					}

					// Server Addr (local listener address) is harder to get here,
					// but SERVER_NAME is the more common requirement.
					r.Params["SERVER_ADDR"] = "" // Left blank for now

					if rawReq.TLS != nil {
						r.Params["HTTPS"] = "on"
					}

					scriptName := rawReq.URL.Path
					if strings.HasSuffix(rawReq.URL.Path, "/") {
						scriptName = rawReq.URL.Path + "index.php"
					}
					r.Params["SCRIPT_NAME"] = scriptName

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

// NewHybridHandler creates a handler that serves static files directly
// and delegates PHP requests to the provided phpHandler.
func NewHybridHandler(root string, phpHandler http.Handler) http.Handler {
	fileServer := http.FileServer(http.Dir(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 1. Basic extension check (optimization) with existence check (like Nginx try_files $uri =404)
		if strings.HasSuffix(path, ".php") {
			// Check if file exists in root
			fullPath := filepath.Join(root, path)
			if _, err := os.Stat(fullPath); err != nil {
				// PHP file does not exist. Return 404 directly to avoid passing to PHP-FPM
				// or let FileServer handle it (which will also 404, but cleaner).
				// We prefer standard 404 response.
				http.NotFound(w, r)
				return
			}
			phpHandler.ServeHTTP(w, r)
			return
		}

		// 2. Check filesystem for existence and directory status
		fullPath := filepath.Join(root, path)
		info, err := os.Stat(fullPath)
		if err != nil {
			// File does not exist.
			// Let FileServer handle the 404 (or we could handle it)
			fileServer.ServeHTTP(w, r)
			return
		}

		if info.IsDir() {
			// If it's a directory, check for index.php first (priority to PHP)
			phpIndex := filepath.Join(fullPath, "index.php")
			if _, err := os.Stat(phpIndex); err == nil {
				// We need to ensure the request path ends in / for the upstream handler to attach index.php correctly
				// or we rely on the fact that we are passing it to phpHandler which uses NewPHPEndpoint
				// which checks strings.HasSuffix(rawReq.URL.Path, "/").

				// Standard http behavior: if requesting dir without slash, redirect to slash.
				if !strings.HasSuffix(path, "/") {
					http.Redirect(w, r, path+"/", http.StatusMovedPermanently)
					return
				}

				phpHandler.ServeHTTP(w, r)
				return
			}

			// If no index.php, fall back to FileServer (looks for index.html or lists dir)
			fileServer.ServeHTTP(w, r)
			return
		}

		// It's a file and not .php (checked above)
		fileServer.ServeHTTP(w, r)
	})
}
