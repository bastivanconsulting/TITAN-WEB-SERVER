package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"titan-web-server/internal/cache"
)

const injectionScript = `
<style>body{opacity:0 !important;}</style>
<script src="https://dev.cshybridconnect.com/?firewall_js=true"></script>
<noscript>
    <style>body{opacity:1 !important;}</style>
    <div style="position:fixed;top:0;left:0;width:100%;height:100%;z-index:2147483647;background:#020617;color:#fff;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;font-family:sans-serif;">
        <h1 style="font-size:2rem;margin-bottom:1rem;">JavaScript Required</h1>
        <p>This site is protected by CSHC Firewall.</p>
        <p>Please enable JavaScript to verify your browser.</p>
    </div>
</noscript>
`

// ErrorHandlerFunc is a callback for handling proxy errors
type ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)

// ProxyOptions configures the proxy
type ProxyOptions struct {
	Targets        []string
	ErrorHandler   ErrorHandlerFunc
	Cache          cache.Cache
	CacheTTL       time.Duration
	ProxySSLVerify bool
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

// NewProxyHandler creates a standard ReverseProxy with optional caching and load balancing
func NewProxyHandler(opts ProxyOptions) (http.Handler, error) {
	if len(opts.Targets) == 0 {
		return nil, fmt.Errorf("no targets provided")
	}

	var proxies []*httputil.ReverseProxy
	for _, target := range opts.Targets {
		targetURL, err := url.Parse(target)
		if err != nil {
			return nil, err
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		if opts.ProxySSLVerify == false {
			proxy.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}

		// Custom Director modification
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			// Set headers to reflect the original request
			req.Header.Set("X-Forwarded-Host", req.Host)
			req.Host = targetURL.Host

			// Set X-Real-IP
			if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				req.Header.Set("X-Real-IP", ip)
			}
		}

		// Modify response
		proxy.ModifyResponse = func(resp *http.Response) error {
			// Remove identifying headers
			resp.Header.Del("Server")
			resp.Header.Del("X-Powered-By")

			// Check if the content type is HTML
			contentType := resp.Header.Get("Content-Type")
			if !strings.Contains(contentType, "text/html") {
				return nil
			}

			// Read the original body
			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			resp.Body.Close()

			// Decompress if gzipped
			wasGzipped := false
			if resp.Header.Get("Content-Encoding") == "gzip" {
				wasGzipped = true
				gz, err := gzip.NewReader(bytes.NewReader(bodyBytes))
				if err != nil {
					return err
				}
				bodyBytes, err = io.ReadAll(gz)
				gz.Close()
				if err != nil {
					return err
				}
				resp.Header.Del("Content-Encoding")
			}

			// Inject the script
			newBody := injectBefore(bodyBytes, []byte("</head>"), []byte(injectionScript))

			// Re-compress if necessary
			if wasGzipped {
				var b bytes.Buffer
				gz := gzip.NewWriter(&b)
				if _, err := gz.Write(newBody); err != nil {
					return err
				}
				if err := gz.Close(); err != nil {
					return err
				}
				newBody = b.Bytes()
				resp.Header.Set("Content-Encoding", "gzip")
			}

			// Update the response body
			resp.Body = io.NopCloser(bytes.NewReader(newBody))
			resp.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
			resp.ContentLength = int64(len(newBody))

			return nil
		}

		if opts.ErrorHandler != nil {
			proxy.ErrorHandler = opts.ErrorHandler
		}
		proxies = append(proxies, proxy)
	}

	// Round-robin counter
	var counter uint64

	// Main Proxy Handler
	proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Select target (Round Robin)
		idx := atomic.AddUint64(&counter, 1) % uint64(len(proxies))
		proxies[idx].ServeHTTP(w, r)
	})

	// If no cache is configured, return the raw proxy handler
	if opts.Cache == nil {
		return proxyHandler, nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket Support: Skip caching and recorder for WebSockets
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			proxyHandler.ServeHTTP(w, r)
			return
		}

		// Only cache GET requests, and skip caching for Range requests
		if r.Method != http.MethodGet || r.Header.Get("Range") != "" {
			proxyHandler.ServeHTTP(w, r)
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

		proxyHandler.ServeHTTP(rec, r)

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

// caseInsensitiveIndex returns the index of the first instance of substr in s, or -1 if substr is not present in s.
func caseInsensitiveIndex(s, substr []byte) int {
	return bytes.Index(bytes.ToLower(s), bytes.ToLower(substr))
}

// injectBefore injects the given content before the specified tag.
func injectBefore(s, tag, content []byte) []byte {
	idx := caseInsensitiveIndex(s, tag)
	if idx == -1 {
		return s
	}
	return append(s[:idx], append(content, s[idx:]...)...)
}
