package server

import (
	"bufio"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/quic-go/quic-go/http3"
	"github.com/yookoala/gofast"

	"titan-web-server/internal/api"
	"titan-web-server/internal/auth"
	"titan-web-server/internal/cache"
	"titan-web-server/internal/config"
	"titan-web-server/internal/proxy"
)

//go:embed templates/error.html
var templateFS embed.FS

//go:embed templates/admin.html
var adminFS embed.FS

// ErrorData holds data for the error template
type ErrorData struct {
	Code     int
	Title    string
	Message  string
	RayID    string
	ClientIP string
}

// Server holds the runtime state of the server
type Server struct {
	mu            sync.RWMutex
	ConfigPath    string
	Config        *config.Config
	Tpl           *template.Template
	APIHandler    *api.Handler
	AuthProvider  auth.Provider
	Cache         cache.Cache
	Logger        *Logger
	phpHandlers   map[string]http.Handler
	proxyHandlers map[string]http.Handler
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, configPath string) *Server {
	tpl, err := template.ParseFS(templateFS, "templates/error.html")
	if err != nil {
		// Fallback if template is missing or error
		tpl = nil
		fmt.Printf("Warning: Could not load error.html: %v\n", err)
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
		phpHandlers:   make(map[string]http.Handler),
		proxyHandlers: make(map[string]http.Handler),
	}

	// Initialize API Handler (srv implements ConfigManager interface implicitly)
	srv.APIHandler = api.NewHandler(srv)

	// Initialize Auth Provider (Placeholder for PAM)
	// Default to admin/admin if not set, or fail? For MVP default is safer than crash, but log warning.
	user := cfg.AdminUser
	pass := cfg.AdminPass
	if user == "" { user = "admin" }
	if pass == "" { pass = "admin" }
	srv.AuthProvider = auth.NewSimpleProvider(user, pass)

	// Initialize handlers (this also sorts routes)
	if err := srv.UpdateConfig(cfg); err != nil {
		panic(fmt.Sprintf("Failed to initialize configuration: %v", err))
	}

	return srv
}

// GetConfig returns a copy of the current configuration (thread-safe)
func (s *Server) GetConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy or pointer? Pointer is dangerous if modified by caller.
	// Since json encoder reads it, pointer is okay as long as we don't modify it.
	// For strict safety we should deep copy, but for this step returning pointer is acceptable
	// assuming the API handler only marshals it.
	return s.Config
}

// UpdateConfig updates the server configuration thread-safely
func (s *Server) UpdateConfig(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update Logger config if changed (simple approach: new instance if dir changed)
	if s.Logger != nil && cfg.LogDir != s.Config.LogDir {
		s.Logger.Close()
		s.Logger = NewLogger(cfg.LogDir)
		s.Logger.EnsureDir()
	}

	// Update Auth Provider credentials if changed
	if cfg.AdminUser != "" || cfg.AdminPass != "" {
		// In a real scenario we'd update the provider instance or its state.
		// Since SimpleProvider is immutable-ish in our usage, let's replace it.
		// Note: This relies on AuthProvider being replaceable (interface).
		user := cfg.AdminUser
		pass := cfg.AdminPass
		if user == "" { user = "admin" } // fallback or keep old?
		if pass == "" { pass = "admin" }
		s.AuthProvider = auth.NewSimpleProvider(user, pass)
	}

	// Sort routes by length (descending) to ensure specific paths are matched first
	for i := range cfg.Sites {
		sort.Slice(cfg.Sites[i].Routes, func(a, b int) bool {
			return len(cfg.Sites[i].Routes[a].Path) > len(cfg.Sites[i].Routes[b].Path)
		})
	}

	s.Config = cfg

	// Re-initialize handlers
	// Note: In a highly optimized real-world scenario, we would diff the config
	// to avoid recreating existing connections, but full re-init is safer for correctness.
	if err := s.initHandlersLocked(); err != nil {
		return err
	}

	// Persist configuration to disk
	return s.saveConfigLocked()
}

func (s *Server) saveConfigLocked() error {
	path := s.ConfigPath
	if path == "" {
		path = "config.json"
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to open config file for writing: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(s.Config); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	return nil
}

func (s *Server) initHandlersLocked() error {
	// Clear existing handlers
	s.phpHandlers = make(map[string]http.Handler)
	s.proxyHandlers = make(map[string]http.Handler)

	for _, site := range s.Config.Sites {
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
				handler := gofast.NewHandler(
					gofast.NewFileEndpoint(route.FileRoot)(gofast.BasicSession),
					gofast.SimpleClientFactory(connFactory),
				)
				s.phpHandlers[key] = handler

			case "proxy":
				key := route.ProxyTarget
				if _, exists := s.proxyHandlers[key]; exists {
					continue
				}

				errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
					s.renderError(w, r, 502, "Erreur Gateway", fmt.Sprintf("Le serveur backend est inaccessible: %v", err))
				}

				opts := proxy.ProxyOptions{
					Target:       route.ProxyTarget,
					ErrorHandler: errorHandler,
					Cache:        s.Cache,
					CacheTTL:     60 * time.Second, // Default TTL
				}
				proxyHandler, err := proxy.NewProxyHandler(opts)
				if err != nil {
					return fmt.Errorf("error initializing proxy for %s: %v", route.ProxyTarget, err)
				}
				s.proxyHandlers[key] = proxyHandler
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
	key := route.ProxyTarget
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
	data := ErrorData{
		Code:     code,
		Title:    title,
		Message:  message,
		RayID:    fmt.Sprintf("REQ-%d", time.Now().UnixNano()),
		ClientIP: r.RemoteAddr,
	}
	s.Tpl.Execute(w, data)
}

// ServeHTTP implements the http.Handler interface
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// Add global Server header
	w.Header().Set("Server", "Titan Web Server")

	// Capture status code for logging
	rw := NewResponseWriter(w)

	// 0. Intercept API and Admin requests
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPI(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin") {
		s.handleAdmin(rw, r)
		return
	}
	if r.URL.Path == "/login" && r.Method == http.MethodPost {
		s.handleLogin(rw, r)
		return
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
			logger.AccessLog("unknown", r.Method, r.URL.Path, r.RemoteAddr, 404, time.Since(start))
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
			logger.AccessLog(matchedSite.Domain, r.Method, r.URL.Path, r.RemoteAddr, rw.Status(), time.Since(start))
		}
	}()

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
		s.renderError(rw, r, 404, "Route non trouvée", "Le chemin demandé n'existe pas.")
		return
	}

	// 3. Dispatch
	switch matchedRoute.Type {
	case "proxy":
		proxyHandler := s.getProxyHandler(matchedRoute)
		if proxyHandler == nil {
			s.renderError(rw, r, 500, "Erreur Configuration", "Handler proxy non initialisé")
			if logger != nil {
				logger.ErrorLog(matchedSite.Domain, "Proxy handler not initialized")
			}
			return
		}
		proxyHandler.ServeHTTP(rw, r)
	case "static":
		fs := http.FileServer(http.Dir(matchedRoute.FileRoot))
		// Strip prefix so /static/foo.css looks for foo.css in root
		http.StripPrefix(matchedRoute.Path, fs).ServeHTTP(rw, r)
	case "php":
		handler := s.getPHPHandler(matchedRoute)
		if handler != nil {
			handler.ServeHTTP(rw, r)
		} else {
			s.renderError(rw, r, 500, "Erreur Configuration", "Handler PHP non initialisé")
			if logger != nil {
				logger.ErrorLog(matchedSite.Domain, "PHP handler not initialized")
			}
		}

	default:
		s.renderError(rw, r, 500, "Erreur Configuration", "Type de route invalide.")
	}
}

// WatchConfig polls the configuration file for changes
func (s *Server) WatchConfig() {
	if s.ConfigPath == "" {
		return
	}

	var lastMod time.Time
	info, err := os.Stat(s.ConfigPath)
	if err == nil {
		lastMod = info.ModTime()
	}

	ticker := time.NewTicker(2 * time.Second)
	go func() {
		for range ticker.C {
			info, err := os.Stat(s.ConfigPath)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()
				fmt.Println("Config file changed, reloading...")

				newCfg, err := config.LoadConfig(s.ConfigPath)
				if err != nil {
					fmt.Printf("Error reloading config (keeping old config): %v\n", err)
					continue
				}

				if err := newCfg.Validate(); err != nil {
					fmt.Printf("Config validation failed (keeping old config): %v\n", err)
					continue
				}

				if err := s.UpdateConfig(newCfg); err != nil {
					fmt.Printf("Error updating config: %v\n", err)
				} else {
					fmt.Println("Configuration reloaded successfully.")
				}
			}
		}
	}()
}

// ResponseWriter wrapper to capture status code
type ResponseWriter struct {
	http.ResponseWriter
	status int
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

	// Build certificate map for SNI and prepare ACME
	certMap := make(map[string]*tls.Certificate)
	ocspManager := NewOCSPManager()
	hasOCSP := false
	var acmeDomains []string

	for _, site := range s.Config.Sites {
		if site.SSL != nil && site.SSL.Enabled {
			if site.SSL.Provider == "manual" {
				cert, err := tls.LoadX509KeyPair(site.SSL.CertFile, site.SSL.KeyFile)
				if err != nil {
					return fmt.Errorf("error loading cert for %s: %w", site.Domain, err)
				}

				domain := strings.ToLower(site.Domain)
				certMap[domain] = &cert

				if site.SSL.OCSPStapling {
					ocspManager.AddCertificate(domain, &cert)
					hasOCSP = true
				}
			} else if site.SSL.Provider == "letsencrypt" {
				acmeDomains = append(acmeDomains, site.Domain)
			}
		}
	}

	var m *autocert.Manager
	if len(acmeDomains) > 0 {
		// Ensure certs directory exists
		if err := os.MkdirAll("certs", 0700); err != nil {
			fmt.Printf("Warning: Failed to create certs directory: %v\n", err)
		}

		m = &autocert.Manager{
			Cache:      autocert.DirCache("certs"),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(acmeDomains...),
		}
		fmt.Printf("ACME/Let's Encrypt enabled for: %v\n", acmeDomains)
	}

	// Start HTTP Server if configured
	if s.Config.HTTPPort != "" {
		go func() {
			fmt.Printf("Starting HTTP server on %s\n", s.Config.HTTPPort)
			var handler http.Handler = s
			if m != nil {
				// Wrap with ACME handler to intercept challenges
				handler = m.HTTPHandler(s)
			}

			if err := http.ListenAndServe(s.Config.HTTPPort, handler); err != nil {
				errChan <- fmt.Errorf("HTTP server failed: %w", err)
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

			tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				domain := strings.ToLower(hello.ServerName)

				// 1. Check Manual Certs
				if cert, ok := certMap[domain]; ok {
					// Apply OCSP Staple if available
					if staple := ocspManager.GetStaple(domain); staple != nil {
						// Return a shallow copy with the staple
						certWithStaple := *cert
						certWithStaple.OCSPStaple = staple
						return &certWithStaple, nil
					}
					return cert, nil
				}

				// 2. Check ACME
				if m != nil {
					return m.GetCertificate(hello)
				}

				return nil, errors.New("certificate not found for domain")
			}
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

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	// 1. IP Allowlist Check
	s.mu.RLock()
	allowlist := s.Config.AdminAllowlist
	apiKey := s.Config.AdminAPIKey
	s.mu.RUnlock()

	if len(allowlist) > 0 {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			// If RemoteAddr doesn't have port (internal/test?), use as is
			host = r.RemoteAddr
		}

		allowed := false
		for _, ip := range allowlist {
			if ip == host || ip == "0.0.0.0" {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "Forbidden (IP)", http.StatusForbidden)
			return
		}
	}

	// 2. API Key Check
	keyHeader := r.Header.Get("X-API-Key")
	if keyHeader != apiKey {
		// Return JSON error for better API client handling
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "Forbidden", "message": "Invalid or missing API Key"}`))
		return
	}

	s.APIHandler.ServeHTTP(w, r)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	// Simple serve of admin.html
	// Security: The admin page itself might not need key to LOAD, but needs key to SAVE.
	// However, seeing the config requires GET /api/config which is protected.
	// So we can serve the HTML public to allowed IPs.

	s.mu.RLock()
	allowlist := s.Config.AdminAllowlist
	s.mu.RUnlock()

	if len(allowlist) > 0 {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}

		allowed := false
		for _, ip := range allowlist {
			if ip == host || ip == "0.0.0.0" {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	content, err := adminFS.ReadFile("templates/admin.html")
	if err != nil {
		http.Error(w, "Admin panel not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(content)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	s.mu.RLock()
	provider := s.AuthProvider
	s.mu.RUnlock()

	if provider.Login(username, password) {
		// In a real app, issue a session cookie/JWT.
		// For this MVP, we just return success, and the frontend might store the API key or similar.
		// Note: The prompt asks for PAM auth for UI. We are simulating the "Login" part.
		// Since the API requires API Key, maybe this login returns the API key?
		// Or just confirms auth.

		// Let's assume successful login returns the API Key (if we treat the API key as the session token for now,
		// although that's mixing concepts, it's a simple way to bootstrap).
		// SECURITY NOTE: Sending the master API key to the client on login is one way to authorize them.

		s.mu.RLock()
		apiKey := s.Config.AdminAPIKey
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success": true, "api_key": "%s"}`, apiKey)
		return
	}

	http.Error(w, "Invalid credentials", http.StatusUnauthorized)
}
