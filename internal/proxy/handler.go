package proxy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"custom-proxy/internal/cache"
)

// ErrorHandlerFunc is a callback for handling proxy errors
type ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)

// ProxyOptions configures the proxy
type ProxyOptions struct {
	Target       string
	ErrorHandler ErrorHandlerFunc
	Cache        cache.Cache
	CacheTTL     time.Duration
}

// limitRecorder captures the response status code, headers and body up to a limit
type limitRecorder struct {
	http.ResponseWriter
	statusCode  int
	body        *bytes.Buffer
	limit       int
	overflow    bool
	wroteHeader bool
}

func (rec *limitRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.statusCode = code
		rec.ResponseWriter.WriteHeader(code)
		rec.wroteHeader = true
	}
}

func (rec *limitRecorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}

	// Capture body if within limit
	if !rec.overflow {
		if rec.body.Len()+len(b) > rec.limit {
			rec.overflow = true
			// Stop capturing but keep writing to client
		} else {
			rec.body.Write(b)
		}
	}

	return rec.ResponseWriter.Write(b)
}

// Flush implements http.Flusher
func (rec *limitRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// NewProxyHandler creates a standard ReverseProxy with optional caching
func NewProxyHandler(opts ProxyOptions) (http.Handler, error) {
	targetURL, err := url.Parse(opts.Target)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Custom Director modification
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("X-Forwarded-Host", req.Host)
	}

	if opts.ErrorHandler != nil {
		proxy.ErrorHandler = opts.ErrorHandler
	}

	// If no cache is configured, return the raw proxy
	if opts.Cache == nil {
		return proxy, nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only cache GET requests
		if r.Method != http.MethodGet {
			proxy.ServeHTTP(w, r)
			return
		}

		// Generate Cache Key
		cacheKey := generateCacheKey(r)

		// Check Cache
		if entry, err := opts.Cache.Get(cacheKey); err == nil {
			// Cache Hit
			for k, v := range entry.Headers {
				for _, val := range v {
					w.Header().Add(k, val)
				}
			}
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Body)
			return
		}

		// Cache Miss - Capture Response
		const maxCacheSize = 10 * 1024 * 1024 // 10MB Limit

		rec := &limitRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default if WriteHeader not called
			body:           &bytes.Buffer{},
			limit:          maxCacheSize,
		}

		proxy.ServeHTTP(rec, r)

		// Cache valid responses if no overflow
		if !rec.overflow && rec.statusCode >= 200 && rec.statusCode < 300 {
			// Check Cache-Control headers
			cc := rec.ResponseWriter.Header().Get("Cache-Control")
			if strings.Contains(cc, "private") || strings.Contains(cc, "no-store") || strings.Contains(cc, "no-cache") {
				return
			}

			// Check Set-Cookie
			if rec.ResponseWriter.Header().Get("Set-Cookie") != "" {
				return
			}

			// Check Vary (Simplification: Do not cache if Vary is present to avoid complexity)
			// A real implementation would include Vary headers in the cache key.
			if rec.ResponseWriter.Header().Get("Vary") != "" {
				return
			}

			// Use default TTL if not set
			ttl := opts.CacheTTL
			if ttl == 0 {
				ttl = 60 * time.Second
			}
			opts.Cache.Set(cacheKey, rec.body.Bytes(), rec.statusCode, rec.ResponseWriter.Header(), ttl)
		}
	}), nil
}

func generateCacheKey(r *http.Request) string {
	// Include Host and URL in the key
	data := fmt.Sprintf("%s|%s", r.Host, r.URL.String())
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}
