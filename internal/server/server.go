package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/quic-go/quic-go/http3"
	"github.com/yookoala/gofast"

	"titan-web-server/internal/auth"
	"titan-web-server/internal/cache"
	"titan-web-server/internal/cluster"
	"titan-web-server/internal/config"
	"titan-web-server/internal/htaccess"
	"titan-web-server/internal/module"
	"titan-web-server/internal/proxy"
	"titan-web-server/internal/tlsutil"
	"titan-web-server/internal/waf"
)

func (s *Server) debugLog(format string, v ...interface{}) {
	if s.Debug {
		log.Printf("[DEBUG] "+format, v...)
	}
}


//go:embed templates/error_4xx.html templates/error_5xx.html
var templateFS embed.FS

// A context key for the request ID
type contextKey string

const requestIDKey contextKey = "requestID"

// ErrorData holds data for the error template
type ErrorData struct {
	Code      int
	Title     string
	Message   string
	RequestID string
	ClientIP  string
}

// Server holds the runtime state of the server
type Server struct {
	mu            sync.RWMutex
	ConfigPath    string
	Config        *config.Config
	Tpl           *template.Template
	Cache         cache.Cache
	Logger        *Logger
	Debug         bool
	ClusterNode   *cluster.Node // Optional cluster node
	phpHandlers   map[string]http.Handler
	proxyHandlers map[string]http.Handler
	wafInstances  map[string]*waf.WAF
	jsModules     map[string]*module.JSModule
	htaccess      map[string]*htaccess.HTAccess
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Generate a unique request ID
	var requestID string
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		requestID = hex.EncodeToString(bytes)
	} else {
		// Fallback to a timestamp-based ID
		requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	// Add request ID to the context
	ctx := context.WithValue(r.Context(), requestIDKey, requestID)
	r = r.WithContext(ctx)

	// Add global Server header
	if isContainerized() {
		w.Header().Set("Server", "Titan Web Container")
	} else {
		w.Header().Set("Server", "Titan Web Server")
	}

	// Capture status code for logging
	rw := NewResponseWriter(w)

	// HTTPS Redirect
	if r.TLS == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, site := range s.Config.Sites {
			if site.Domain == r.Host && site.SSL != nil && site.SSL.Enabled {
				target := "https://" + r.Host + r.URL.Path
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
				return
			}
		}
	}


	// 1. Match Host
	host := r.Host
	// Remove port if present
	if strings.Contains(host, ":") {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			// Fallback if parsing fails (e.g. might be just IPv6 without brackets? rarely happens in Host header)
			// But for safety, keep original string if Split fails unless empty
			if strings.Contains(r.Host, ":") {
				// simple fallback for IPv4/Hostname
				host = strings.Split(r.Host, ":")[0]
			}
		}
	}

	// Read config safely
	s.mu.RLock()
	cfg := s.Config
	logger := s.Logger
	s.mu.RUnlock()

	var matchedSite *config.Site
	for _, site := range cfg.Sites {
		if site.Domain == host {
			matchedSite = &site
			break
		}
	}

	if matchedSite == nil {
		s.renderError(rw, r, 404, "Site non trouvé", "Le domaine demandé n'est pas configuré sur ce serveur.")
		// Log 404 for unknown host
		if logger != nil {
			requestID, _ := r.Context().Value(requestIDKey).(string)
			logger.AccessLog("unknown", requestID, r.Method, r.URL.String(), r.RemoteAddr, 404, time.Since(start))
		}
		return
	}

	// HSTS Handling
	if matchedSite.SSL != nil && matchedSite.SSL.Enabled && matchedSite.SSL.HSTS != nil && matchedSite.SSL.HSTS.Enabled {
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			val := fmt.Sprintf("max-age=%d", matchedSite.SSL.HSTS.MaxAge)
			if matchedSite.SSL.HSTS.IncludeSubDomains {
				val += "; includeSubDomains"
			}
			if matchedSite.SSL.HSTS.Preload {
				val += "; preload"
			}
			rw.Header().Set("Strict-Transport-Security", val)
		}
	}

	defer func() {
		// Access Log
		if logger != nil {
			requestID, _ := r.Context().Value(requestIDKey).(string)
			logger.AccessLog(matchedSite.Domain, requestID, r.Method, r.URL.String(), r.RemoteAddr, rw.Status(), time.Since(start))
		}
	}()

	// Execute JS module if configured
	var jsModule *module.JSModule
	if matchedSite.JSModule != "" {
		s.mu.RLock()
		jsModule = s.jsModules[matchedSite.JSModule]
		s.mu.RUnlock()
	}

	// The core routing and dispatching logic is defined as a handler.
	coreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2. Match Route (Longest prefix match is usually best, but simple first match for now)
		// We should ideally sort routes by length descending to avoid prefix collisions.
		var matchedRoute *config.Route
		for _, route := range matchedSite.Routes {
			if strings.HasPrefix(r.URL.Path, route.Path) {
				matchedRoute = &route
				break
			}
		}

		if matchedRoute == nil {
			s.renderError(w, r, 404, "Route non trouvée", "Le chemin demandé n'existe pas.")
			return
		}

		// Override site-level JS module if route-level is set
		if matchedRoute.JSModule != "" {
			s.mu.RLock()
			jsModule = s.jsModules[matchedRoute.JSModule]
			s.mu.RUnlock()
		}

		// Apply .htaccess rewrite rules
		if (matchedRoute.Type == "static" || matchedRoute.Type == "php") && matchedRoute.FileRoot != "" {
			htaccessPath := matchedRoute.FileRoot + "/.htaccess"
			s.mu.RLock()
			hta, exists := s.htaccess[htaccessPath]
			s.mu.RUnlock()
			if exists {
				newURI := hta.Evaluate(r.URL.Path, r.Header)
				if newURI != r.URL.Path {
					r.URL.Path = newURI
				}
			}
		}

		// Execute JS module if it exists
		if jsModule != nil {
			err := jsModule.Run(r, w)
			if err != nil {
				s.renderError(w, r, 500, "Erreur de module", fmt.Sprintf("Le module JavaScript a rencontré une erreur: %v", err))
				return
			}
		}

		// Apply Basic Auth if configured
		if matchedRoute.AuthBasicUserFile != "" {
			auth.BasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// 3. Dispatch
				s.dispatch(w, r, matchedRoute, matchedSite.Domain)
			}), matchedRoute.AuthBasic, matchedRoute.AuthBasicUserFile).ServeHTTP(w, r)
			return
		}

		// 3. Dispatch
		s.dispatch(w, r, matchedRoute, matchedSite.Domain)
	})

	// Wrap with WAF if enabled for this site
	s.mu.RLock()
	siteWAF, wafEnabled := s.wafInstances[matchedSite.Domain]
	s.mu.RUnlock()

	var finalHandler http.Handler = coreHandler
	if wafEnabled {
		finalHandler = siteWAF.Middleware(finalHandler)
	}

	if matchedSite.Gzip {
		finalHandler = GzipMiddleware(finalHandler)
	}

	finalHandler.ServeHTTP(rw, r)
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, configPath string, debug bool) *Server {
	tpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		// Fallback if template is missing or error
		tpl = nil
		fmt.Printf("Warning: Could not load error templates: %v\n", err)
	}

	// Initialize Cache
	var c cache.Cache
	if cfg.Cache != nil {
		if cfg.Cache.Type == "disk" {
			var err error
			c, err = cache.NewDiskCache(cfg.Cache.Path)
			if err != nil {
				fmt.Printf("Error initializing disk cache: %v. Caching disabled.\n", err)
				c = nil
			} else {
				fmt.Printf("Initialized disk cache at %s\n", cfg.Cache.Path)
			}
		} else if cfg.Cache.Type == "memory" {
			c = cache.NewMemoryCache()
			fmt.Println("Initialized memory cache")
		} else {
			fmt.Printf("Unknown cache type '%s'. Caching disabled.\n", cfg.Cache.Type)
		}
	} else {
		fmt.Println("Cache configuration missing. Caching disabled.")
	}

	// Initialize Logger
	logger := NewLogger(cfg.LogDir)
	if err := logger.EnsureDir(); err != nil {
		fmt.Printf("Warning: Failed to create log directory: %v\n", err)
	}

	srv := &Server{
		Config:        cfg,
		ConfigPath:    configPath,
		Tpl:           tpl,
		Cache:         c,
		Logger:        logger,
		Debug:         debug,
		phpHandlers:   make(map[string]http.Handler),
		proxyHandlers: make(map[string]http.Handler),
		wafInstances:  make(map[string]*waf.WAF),
		jsModules:     make(map[string]*module.JSModule),
		htaccess:      make(map[string]*htaccess.HTAccess),
	}

	// Initialize handlers (this also sorts routes)
	if err := srv.ApplyConfig(cfg); err != nil {
		panic(fmt.Sprintf("Failed to initialize configuration: %v", err))
	}

	return srv
}

// ApplyConfig performs the actual update of the runtime state (internal use or via Raft)
func (s *Server) ApplyConfig(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update Logger config if changed (simple approach: new instance if dir changed)
	if s.Logger != nil && cfg.LogDir != s.Config.LogDir {
		s.Logger.Close()
		s.Logger = NewLogger(cfg.LogDir)
		s.Logger.EnsureDir()
	}

	// Sort routes
	for i := range cfg.Sites {
		sort.Slice(cfg.Sites[i].Routes, func(a, b int) bool {
			return len(cfg.Sites[i].Routes[a].Path) > len(cfg.Sites[i].Routes[b].Path)
		})
	}

	s.Config = cfg

	// Re-initialize handlers
	if err := s.initHandlersLocked(); err != nil {
		return err
	}

	return nil
}


func (s *Server) initHandlersLocked() error {
	// Clear existing handlers
	s.phpHandlers = make(map[string]http.Handler)
	s.proxyHandlers = make(map[string]http.Handler)

	// Clear and close old WAF instances
	for domain, w := range s.wafInstances {
		w.Close()
		delete(s.wafInstances, domain)
	}

	// Clear old JS modules
	s.jsModules = make(map[string]*module.JSModule)
	s.htaccess = make(map[string]*htaccess.HTAccess)

	for i := range s.Config.Sites {
		site := &s.Config.Sites[i]
		// Determine if a WAF instance is needed for this site.
		// It's needed if the site itself has a WAF block, or any of its locations do.
		needsWAF := site.WAF != nil
		if !needsWAF {
			for _, route := range site.Routes {
				if route.WAF != nil {
					needsWAF = true
					break
				}
			}
		}

		if needsWAF {
			w, err := waf.New(site, s.Config.GeoIPDBPath, s.Logger)
			if err != nil {
				log.Printf("Error initializing WAF for site %s: %v", site.Domain, err)
			} else {
				s.wafInstances[site.Domain] = w
				log.Printf("WAF handler initialized for site: %s", site.Domain)
			}
		}

		if site.JSModule != "" {
			if _, exists := s.jsModules[site.JSModule]; !exists {
				m, err := module.New(site.JSModule)
				if err != nil {
					log.Printf("Error loading JS module for site %s: %v", site.Domain, err)
				} else {
					s.jsModules[site.JSModule] = m
					log.Printf("JS module loaded for site: %s", site.Domain)
				}
			}
		}

		for _, route := range site.Routes {
			switch route.Type {
			case "php":
				key := fmt.Sprintf("%s|%s", route.ProxyTarget, route.FileRoot)
				// Check if already initialized to avoid duplication
				if _, exists := s.phpHandlers[key]; exists {
					continue
				}

				network := "tcp"
				address := "127.0.0.1:9000"
				if route.ProxyTarget != "" {
					if strings.Contains(route.ProxyTarget, "://") {
						parts := strings.SplitN(route.ProxyTarget, "://", 2)
						if len(parts) == 2 {
							network = parts[0]
							address = parts[1]
						}
					} else {
						// Assume TCP address if no scheme provided
						address = route.ProxyTarget
					}
				}

				connFactory := gofast.SimpleConnFactory(network, address)
				phpHandler := gofast.NewHandler(
					NewPHPEndpoint(route.FileRoot)(gofast.BasicSession),
					gofast.SimpleClientFactory(connFactory),
				)
				// Wrap with HybridHandler to support static files
				s.phpHandlers[key] = NewHybridHandler(route.FileRoot, phpHandler)

			case "proxy":
				// Generate key from all upstreams to handle load balancing groups
				key := strings.Join(route.Upstreams, ",")
				if _, exists := s.proxyHandlers[key]; exists {
					continue
				}

				errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
					s.renderError(w, r, 502, "Erreur Gateway", fmt.Sprintf("Le serveur backend est inaccessible: %v", err))
				}

				opts := proxy.ProxyOptions{
					Targets:        route.Upstreams,
					ErrorHandler:   errorHandler,
					Cache:          s.Cache,
					CacheTTL:       60 * time.Second, // Default TTL
					ProxySSLVerify: route.ProxySSLVerify,
				}
				if route.LoadBalancer != nil {
					opts.Algorithm = route.LoadBalancer.Algorithm
					opts.HealthCheck = route.LoadBalancer.HealthCheck
				}
				proxyHandler, err := proxy.NewProxyHandler(opts)
				if err != nil {
					return fmt.Errorf("error initializing proxy for %s: %v", key, err)
				}
				s.proxyHandlers[key] = proxyHandler
			}
			if route.JSModule != "" {
				if _, exists := s.jsModules[route.JSModule]; !exists {
					m, err := module.New(route.JSModule)
					if err != nil {
						log.Printf("Error loading JS module for route %s: %v", route.Path, err)
					} else {
						s.jsModules[route.JSModule] = m
						log.Printf("JS module loaded for route: %s", route.Path)
					}
				}
			}

			if (route.Type == "static" || route.Type == "php") && route.FileRoot != "" {
				htaccessPath := route.FileRoot + "/.htaccess"
				if _, err := os.Stat(htaccessPath); err == nil {
					if _, exists := s.htaccess[htaccessPath]; !exists {
						hta, err := htaccess.Parse(htaccessPath)
						if err != nil {
							log.Printf("Error parsing .htaccess file for route %s: %v", route.Path, err)
						} else {
							s.htaccess[htaccessPath] = hta
							log.Printf(".htaccess file loaded for route: %s", route.Path)
						}
					}
				}
			}
		}
	}
	return nil
}

func (s *Server) getPHPHandler(route *config.Route) http.Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("%s|%s", route.ProxyTarget, route.FileRoot)
	if h, ok := s.phpHandlers[key]; ok {
		return h
	}
	return nil
}

func (s *Server) getProxyHandler(route *config.Route) http.Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := strings.Join(route.Upstreams, ",")
	if h, ok := s.proxyHandlers[key]; ok {
		return h
	}
	return nil
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, code int, title, message string) {
	w.WriteHeader(code)
	if s.Tpl == nil {
		fmt.Fprintf(w, "%d - %s: %s", code, title, message)
		return
	}

	requestID, _ := r.Context().Value(requestIDKey).(string)

	data := ErrorData{
		Code:      code,
		Title:     title,
		Message:   message,
		RequestID: requestID,
		ClientIP:  r.RemoteAddr,
	}

	templateName := "error_4xx.html"
	if code >= 500 {
		templateName = "error_5xx.html"
	}

	s.Tpl.ExecuteTemplate(w, templateName, data)
}

// isContainerized checks if the server is running inside a Docker or Kubernetes environment.
func isContainerized() bool {
	// Check for Docker environment
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check for Kubernetes environment
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}

	return false
}


func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, matchedRoute *config.Route, domain string) {
	if matchedRoute.LimitRate > 0 {
		limiter := rate.NewLimiter(rate.Limit(matchedRoute.LimitRate*1024), 2048*1024) // Burst of 2MB
		w = &rateLimitedResponseWriter{ResponseWriter: w, limiter: limiter, ctx: r.Context()}
	}

	switch matchedRoute.Type {
	case "proxy":
		proxyHandler := s.getProxyHandler(matchedRoute)
		if proxyHandler == nil {
			s.renderError(w, r, 500, "Erreur Configuration", "Handler proxy non initialisé")
			if s.Logger != nil {
				s.Logger.ErrorLog(domain, "Proxy handler not initialized")
			}
			return
		}
		proxyHandler.ServeHTTP(w, r)
	case "static":
		fs := http.FileServer(http.Dir(matchedRoute.FileRoot))
		// Strip prefix so /static/foo.css looks for foo.css in root
		http.StripPrefix(matchedRoute.Path, fs).ServeHTTP(w, r)
	case "php":
		handler := s.getPHPHandler(matchedRoute)
		if handler != nil {
			handler.ServeHTTP(w, r)
		} else {
			s.renderError(w, r, 500, "Erreur Configuration", "Handler PHP non initialisé")
			if s.Logger != nil {
				s.Logger.ErrorLog(domain, "PHP handler not initialized")
			}
		}

	default:
		s.renderError(w, r, 500, "Erreur Configuration", "Type de route invalide.")
	}
}

// ResponseWriter wrapper to capture status code
type ResponseWriter struct {
	http.ResponseWriter
	status int
}

// rateLimitedResponseWriter wraps a http.ResponseWriter to limit the response writing speed.
type rateLimitedResponseWriter struct {
	http.ResponseWriter
	limiter *rate.Limiter
	ctx     context.Context
}

func (w *rateLimitedResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		return n, err
	}
	if err := w.limiter.WaitN(w.ctx, n); err != nil {
		return n, err
	}
	return n, nil
}

func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{w, http.StatusOK}
}

func (rw *ResponseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *ResponseWriter) Status() int {
	return rw.status
}

// Flush implements http.Flusher
func (rw *ResponseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker
func (rw *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Error channel to catch failures from goroutines
	errChan := make(chan error)

	// Build certificate map for SNI
	certMap := make(map[string]*tls.Certificate)
	ocspManager := NewOCSPManager()
	hasOCSP := false

	// Load manual certificates from /etc/titan/certs/manual/
	s.Config.ManualCerts = make(map[string]*tls.Certificate)
	manualCertDir := "/etc/titan/certs/manual/"
	files, err := os.ReadDir(manualCertDir)
	if err == nil {
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".crt") {
				certFile := manualCertDir + file.Name()
				keyFile := manualCertDir + strings.TrimSuffix(file.Name(), ".crt") + ".key"
				if _, err := os.Stat(keyFile); err == nil {
					cert, err := tls.LoadX509KeyPair(certFile, keyFile)
					if err != nil {
						fmt.Printf("Warning: Could not load manual certificate %s: %v\n", file.Name(), err)
						continue
					}
					// TODO: Extract domains from certificate
					// For now, we'll use the filename as the domain
					domain := strings.TrimSuffix(file.Name(), ".crt")
					s.Config.ManualCerts[domain] = &cert
					fmt.Printf("Loaded manual certificate for %s\n", domain)
				}
			}
		}
	}


	// Ensure default self-signed certs exist
	defaultCert := filepath.Join(s.Config.SSLPath, "server.crt")
	defaultKey := filepath.Join(s.Config.SSLPath, "server.key")
	if err := tlsutil.EnsureDefaultCert(defaultCert, defaultKey); err != nil {
		fmt.Printf("Warning: Could not generate default self-signed certs: %v\n", err)
	}

	// Load Default Cert
	var defaultTLSCert *tls.Certificate
	if defCert, err := tls.LoadX509KeyPair(defaultCert, defaultKey); err == nil {
		defaultTLSCert = &defCert
	}

	for _, site := range s.Config.Sites {
		if site.SSL != nil && site.SSL.Enabled {
			if site.SSL.Certificate != "" && site.SSL.CertificateKey != "" {
				cert, err := tls.LoadX509KeyPair(site.SSL.Certificate, site.SSL.CertificateKey)
				if err != nil {
					return fmt.Errorf("error loading cert for %s: %w", site.Domain, err)
				}

				domain := strings.ToLower(site.Domain)
				certMap[domain] = &cert

				if site.SSL.OCSPStapling {
					ocspManager.AddCertificate(domain, &cert)
					hasOCSP = true
				}
			}
		}
	}

	// Start HTTP Server if configured
	if s.Config.HTTPPort != "" {
		go func() {
			log.Printf("Starting HTTP server on %s\n", s.Config.HTTPPort)
			if err := http.ListenAndServe(s.Config.HTTPPort, s); err != nil {
				errChan <- fmt.Errorf("HTTP server on %s failed: %w", s.Config.HTTPPort, err)
			}
		}()
	}

	// Start HTTPS Server if configured
	if s.Config.HTTPSPort != "" {
		if hasOCSP {
			ocspManager.Start()
		}

		// Always start HTTPS if configured (might be for ACME only)
		go func() {
			var tlsConfig *tls.Config
			if s.Config.Security != nil && s.Config.Security.FIPSCompliant {
				tlsConfig = getFIPSTLSConfig()
				fmt.Println("Security: FIPS Compliance constraints enabled (TLS 1.2+, Strong Ciphers).")
			} else {
				tlsConfig = &tls.Config{}
			}

			tlsConfig.GetConfigForClient = s.getConfigForClient(ocspManager, certMap, defaultTLSCert)
			tlsConfig.NextProtos = []string{"h3", "http/1.1"} // Advertise HTTP/3 support

			// Start HTTP/3 (QUIC) Server
			quicServer := &http3.Server{
				Addr:      s.Config.HTTPSPort,
				Handler:   s,
				TLSConfig: tlsConfig,
			}
			go func() {
				fmt.Printf("Starting HTTP/3 (QUIC) server on %s\n", s.Config.HTTPSPort)
				if err := quicServer.ListenAndServe(); err != nil {
					fmt.Printf("HTTP/3 server error: %v\n", err)
				}
			}()

			// Start HTTPS (TCP) Server
			server := &http.Server{
				Addr:      s.Config.HTTPSPort,
				Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Advertise HTTP/3 via Alt-Svc header
					// Port needs to be extracted from HTTPSPort string (e.g. ":8443" -> "8443")
					port := s.Config.HTTPSPort
					if strings.HasPrefix(port, ":") {
						port = port[1:]
					}
					w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%s"; ma=2592000`, port))
					s.ServeHTTP(w, r)
				}),
				TLSConfig: tlsConfig,
			}

			fmt.Printf("Starting HTTPS server on %s (SNI Enabled)\n", s.Config.HTTPSPort)
			// ListenAndServeTLS with empty strings uses the Config.TLSConfig
			if err := server.ListenAndServeTLS("", ""); err != nil {
				errChan <- fmt.Errorf("HTTPS server failed: %w", err)
			}
		}()
	}

	// Block until a server fails
	return <-errChan
}

func (s *Server) getConfigForClient(ocspManager *OCSPManager, certMap map[string]*tls.Certificate, defaultTLSCert *tls.Certificate) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		// Public-facing certificate logic
		domain := strings.ToLower(hello.ServerName)
		s.debugLog("GetCertificate called for domain: %s", domain)

		// 1. Check Manually loaded certs from /etc/titan/certs/manual
		s.mu.RLock()
		manualCert, ok := s.Config.ManualCerts[domain]
		s.mu.RUnlock()
		if ok {
			s.debugLog("Found manual certificate for %s in /etc/titan/certs/manual/", domain)
			return &tls.Config{Certificates: []tls.Certificate{*manualCert}}, nil
		}

		// 2. Check Manual Certs from config file
		if cert, ok := certMap[domain]; ok {
			s.debugLog("Found manual certificate for %s in config file", domain)
			if staple := ocspManager.GetStaple(domain); staple != nil {
				s.debugLog("Applying OCSP staple for %s", domain)
				certWithStaple := *cert
				certWithStaple.OCSPStaple = staple
				return &tls.Config{Certificates: []tls.Certificate{certWithStaple}}, nil
			}
			return &tls.Config{Certificates: []tls.Certificate{*cert}}, nil
		}


		// 4. Fallback to Default Self-Signed Cert
		if defaultTLSCert != nil {
			s.debugLog("No specific certificate found for %s. Falling back to default self-signed cert.", domain)
			return &tls.Config{Certificates: []tls.Certificate{*defaultTLSCert}}, nil
		}

		s.debugLog("No certificate found for domain %s and no default certificate available.", domain)
		return nil, errors.New("certificate not found for domain")
	}
}
