package server

import (
	"fmt"
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
