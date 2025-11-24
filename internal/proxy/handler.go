package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// ErrorHandlerFunc is a callback for handling proxy errors
type ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)

// NewProxyHandler creates a standard ReverseProxy
func NewProxyHandler(target string, errorHandler ErrorHandlerFunc) (http.Handler, error) {
	url, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	// Custom Director modification if needed (e.g. setting headers)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("X-Forwarded-Host", req.Host)
	}

	if errorHandler != nil {
		proxy.ErrorHandler = errorHandler
	}

	return proxy, nil
}
