package server

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
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
	"titan-web-server/internal/cluster"
	"titan-web-server/internal/config"
	"titan-web-server/internal/proxy"
	"titan-web-server/internal/tlsutil"
	"titan-web-server/internal/waf"
)

func (s *Server) debugLog(format string, v ...interface{}) {
	if s.Debug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// acmeHealthCheckWrapper wraps an http.Handler to add a diagnostic endpoint
// for the ACME challenge. This must be wrapped *before* the autocert handler.
func acmeHealthCheckWrapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/acme-challenge/healthz" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	Debug         bool
	autocertManager *autocert.Manager
	ClusterNode   *cluster.Node // Optional cluster node
	phpHandlers   map[string]http.Handler
	proxyHandlers map[string]http.Handler
	wafInstances  map[string]*waf.WAF
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, configPath string, debug bool) *Server {
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
		Debug:         debug,
		phpHandlers:   make(map[string]http.Handler),
		proxyHandlers: make(map[string]http.Handler),
		wafInstances:  make(map[string]*waf.WAF),
	}

	// Initialize API Handler (srv implements ConfigManager interface implicitly)
	srv.APIHandler = api.NewHandler(srv)

	// Initialize Auth Provider
	// Check if configured for PAM (we can add a config field later, or infer)
	// For now, if AdminUser is "pam", we try PAM. Or we add a field to Config.
	// Since I can't easily change the Config struct without breaking existing JSON,
	// I'll check if AdminUser is "system" or a specific flag.
	// Actually, let's assume if AdminPass is empty but User is set, it might use PAM?
	// No, that's ambiguous.
	// Let's stick to SimpleProvider unless a specific env var or config is set.
	// But the user *requested* PAM.
	// I'll add logic: if AdminUser starts with "pam:", use the rest as service name?
	// Or just try PAM if Simple fails? No.
	// I will modify Config to support `AuthType`.

	if cfg.AdminUser == "pam" {
		pamProvider, err := auth.NewPAMProvider("login") // "login" is a standard service
		if err != nil {
			fmt.Printf("Error initializing PAM: %v. Falling back to simple auth.\n", err)
			srv.AuthProvider = auth.NewSimpleProvider("admin", "admin")
		} else {
			srv.AuthProvider = pamProvider
			fmt.Println("Authentication: PAM enabled (service: login)")
		}
	} else {
		user := cfg.AdminUser
		pass := cfg.AdminPass
		if user == "" { user = "admin" }
		if pass == "" { pass = "admin" }
		srv.AuthProvider = auth.NewSimpleProvider(user, pass)
	}

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
	// If we are part of a cluster and this is a user request (not an internal Raft callback),
	// we should route this through Raft.
	// However, UpdateConfig is called by the Raft callback too.
	// We need to distinguish or ensure we don't infinitely loop.
	// The Raft callback calls s.UpdateConfig.
	// The API handler calls s.UpdateConfig.

	// Implementation:
	// If ClusterNode is present, we should assume the API handler calling this wants to replicate.
	// BUT `UpdateConfig` is also used by `main.go` for initial load and file watcher.
	// We should separate "ProposeConfig" from "ApplyConfig".

	// For now, let's handle the local update logic here.
	// The API Handler should call ProposeConfig if cluster is active.
	// But `UpdateConfig` is the method satisfying `ConfigManager` interface.

	// Let's modify UpdateConfig to check if it should replicate.
	// This is slightly messy. Better to have API handler check.
	// But API handler doesn't know about ClusterNode directly (interface).
	// So we handle it here.

	s.mu.Lock()
	clusterNode := s.ClusterNode
	s.mu.Unlock()

	// If we have a cluster node, we must replicate unless this update came FROM the cluster.
	// But we don't know source here.
	// Simpler: The API will call `UpdateConfig`.
	// If `ClusterNode` is set, `UpdateConfig` should convert to text and call `node.ApplyConfigUpdate`.
	// Then `ApplyConfigUpdate` will go through Raft, then `FSM.Apply` -> `callback` -> `s.applyConfigInternal`.

	// To avoid loop, we need `applyConfigInternal` method.
	// `UpdateConfig` will be the public entry point.

	if clusterNode != nil {
		// We are in a cluster. We must go through Raft.
		confData := config.EncodeToConf(cfg)
		return clusterNode.ApplyConfigUpdate([]byte(confData))
	}

	// Local update only
	return s.ApplyConfig(cfg)
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

	// Update Auth Provider credentials if changed
	if cfg.AdminUser != "" || cfg.AdminPass != "" {
		user := cfg.AdminUser
		pass := cfg.AdminPass
		if user == "" { user = "admin" }
		if pass == "" { pass = "admin" }
		s.AuthProvider = auth.NewSimpleProvider(user, pass)
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

	// Persist configuration to disk
	return s.saveConfigLocked()
}

func (s *Server) saveConfigLocked() error {
	path := s.ConfigPath
	if path == "" {
		path = "titan-server.conf"
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to open config file for writing: %w", err)
	}
	defer file.Close()

	// Use the new encoder
	content := config.EncodeToConf(s.Config)
	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// JoinCluster joins a new node to the cluster
func (s *Server) JoinCluster(nodeID, addr string) error {
	s.mu.RLock()
	node := s.ClusterNode
	s.mu.RUnlock()

	if node == nil {
		return errors.New("clustering not enabled on this node")
	}

	return node.Join(nodeID, addr)
}

// GetClusterStatus returns the status of the cluster
func (s *Server) GetClusterStatus() map[string]interface{} {
	s.mu.RLock()
	node := s.ClusterNode
	s.mu.RUnlock()

	status := make(map[string]interface{})
	if node == nil {
		status["enabled"] = false
		return status
	}

	status["enabled"] = true
	status["leader"] = node.Raft.Leader()
	status["state"] = node.Raft.State().String()

	// We can get more stats from node.Raft.Stats()
	stats := node.Raft.Stats()
	status["stats"] = stats

	// Get peers/servers
	future := node.Raft.GetConfiguration()
	if err := future.Error(); err == nil {
		var servers []map[string]string
		for _, srv := range future.Configuration().Servers {
			servers = append(servers, map[string]string{
				"id": string(srv.ID),
				"address": string(srv.Address),
				"suffrage": srv.Suffrage.String(),
			})
		}
		status["servers"] = servers
	}

	return status
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

	for _, site := range s.Config.Sites {
		// Initialize WAF if enabled for the site
		if site.WAF != nil && site.WAF.Enabled {
			w, err := waf.New(site.WAF, s.Config.GeoIPDBPath, s.Logger, site.Domain)
			if err != nil {
				log.Printf("Error initializing WAF for site %s: %v", site.Domain, err)
			} else {
				s.wafInstances[site.Domain] = w
				log.Printf("WAF enabled for site: %s", site.Domain)
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
					Targets:      route.Upstreams,
					ErrorHandler: errorHandler,
					Cache:        s.Cache,
					CacheTTL:     60 * time.Second, // Default TTL
				}
				proxyHandler, err := proxy.NewProxyHandler(opts)
				if err != nil {
					return fmt.Errorf("error initializing proxy for %s: %v", key, err)
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

	// ACME Health Check for diagnostics
	if r.URL.Path == "/.well-known/acme-challenge/healthz" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	// 0. Intercept API and Admin requests
	// Note: Internal API for cluster sync bypasses normal auth for performance,
	// but relies on the shared API key.
	isCertSync := strings.HasPrefix(r.URL.Path, "/api/internal/certs/")
	isInternalAPI := strings.HasPrefix(r.URL.Path, "/api/internal/")

	// Enforce mTLS at the application layer for internal APIs.
	// The server's TLS config is set to `VerifyClientCertIfGiven` to allow the cert sync
	// bootstrap to work. For all other internal endpoints, we must enforce a valid cert.
	if isInternalAPI && !isCertSync {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "Forbidden: A valid client certificate is required for this endpoint", http.StatusForbidden)
			return
		}
	}


	if isCertSync {
		s.handleCertSync(rw, r)
		return
	}
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

	// Wrap with WAF if enabled for this site
	s.mu.RLock()
	siteWAF, wafEnabled := s.wafInstances[matchedSite.Domain]
	s.mu.RUnlock()

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

		// 3. Dispatch
		switch matchedRoute.Type {
		case "proxy":
			proxyHandler := s.getProxyHandler(matchedRoute)
			if proxyHandler == nil {
				s.renderError(w, r, 500, "Erreur Configuration", "Handler proxy non initialisé")
				if logger != nil {
					logger.ErrorLog(matchedSite.Domain, "Proxy handler not initialized")
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
				if logger != nil {
					logger.ErrorLog(matchedSite.Domain, "PHP handler not initialized")
				}
			}

		default:
			s.renderError(w, r, 500, "Erreur Configuration", "Type de route invalide.")
		}
	})

	var finalHandler http.Handler = coreHandler
	if wafEnabled {
		finalHandler = siteWAF.Middleware(finalHandler)
	}

	finalHandler.ServeHTTP(rw, r)
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
	defaultCert := "server.crt"
	defaultKey := "server.key"
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
		certDir := "/etc/titan/certs"
		if err := os.MkdirAll(certDir, 0700); err != nil {
			fmt.Printf("Warning: Failed to create certs directory: %v\n", err)
		}

		var acmeEmail string
		for _, site := range s.Config.Sites {
			if site.SSL != nil && site.SSL.Provider == "letsencrypt" && site.SSL.Email != "" {
				acmeEmail = site.SSL.Email
				break
			}
		}

		if acmeEmail == "" {
			log.Fatalf("ACME/Let's Encrypt is enabled but no email address is provided in any site configuration. This is required by Let's Encrypt TOS.")
		}

		m = &autocert.Manager{
			Cache:      autocert.DirCache(certDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(acmeDomains...),
			Email:      acmeEmail,
		}
		s.autocertManager = m
		s.debugLog("autocert manager configured with CacheDir: %s, HostPolicy: %v, Email: %s", certDir, acmeDomains, acmeEmail)
		log.Printf("ACME/Let's Encrypt enabled for domains: %v (Email: %s)", acmeDomains, acmeEmail)
	}

	// Start HTTP Server
	httpPort80Configured := s.Config.HTTPPort == ":80" || s.Config.HTTPPort == "80"

	// If user configured an HTTP port, start that server.
	if s.Config.HTTPPort != "" {
		go func() {
			log.Printf("Starting HTTP server on %s\n", s.Config.HTTPPort)
			var handler http.Handler = s
			if m != nil {
				// If the user's server is on port 80, autocert can use it.
				// Otherwise, autocert will need its own server (started below).
				handler = m.HTTPHandler(handler)
			}
			// Always wrap with health check
			finalHandler := acmeHealthCheckWrapper(handler)
			if err := http.ListenAndServe(s.Config.HTTPPort, finalHandler); err != nil {
				errChan <- fmt.Errorf("HTTP server on %s failed: %w", s.Config.HTTPPort, err)
			}
		}()
	}

	// If ACME is enabled and no server is running on port 80, start one.
	// This is required for the HTTP-01 challenge.
	if m != nil && !httpPort80Configured {
		go func() {
			log.Println("Starting dedicated ACME challenge server on :80")
			// m.HTTPHandler(nil) handles challenges and redirects other HTTP -> HTTPS
			handler := m.HTTPHandler(nil)
			finalHandler := acmeHealthCheckWrapper(handler)
			err := http.ListenAndServe(":80", finalHandler)
			if err != nil {
				// Don't fail fatally, but log a clear warning.
				log.Printf("WARNING: Could not start ACME challenge server on port 80: %v. Let's Encrypt may fail.", err)
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

func (s *Server) handleCertSync(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	node := s.ClusterNode
	apiKey := s.Config.AdminAPIKey
	s.mu.RUnlock()

	if node == nil {
		http.Error(w, "Clustering not enabled", http.StatusServiceUnavailable)
		return
	}

	// Authenticate request
	if r.Header.Get("X-API-Key") != apiKey {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch r.URL.Path {
	case cluster.SyncEndpoint:
		// This is a follower receiving a manifest from the leader.
		// We use r.RemoteAddr to get the leader's actual address, as node.Raft.Leader()
		// might return a Raft-specific address that isn't reachable over HTTPS.
		leaderAddr := r.RemoteAddr
		node.HandleSyncRequest(w, r, leaderAddr, apiKey, s.Config.HTTPSPort)
	case cluster.FileEndpoint:
		// This is the leader serving a file to a follower
		node.HandleFileRequest(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) RenewLetsEncrypt(domain string) error {
	s.mu.RLock()
	m := s.autocertManager
	s.mu.RUnlock()

	if m == nil {
		return errors.New("Let's Encrypt is not enabled")
	}

	s.debugLog("Manual renewal triggered for domain: %s", domain)

	// This is a bit of a hack. Autocert doesn't expose a direct "renew" method.
	// We can force it by creating a new ClientHello and calling GetCertificate.
	// This will trigger the renewal logic if the cert is missing or expiring.
	go func() {
		hello := &tls.ClientHelloInfo{ServerName: domain}
		_, err := m.GetCertificate(hello)
		if err != nil {
			s.debugLog("Manual renewal for %s failed: %v", domain, err)
		} else {
			s.debugLog("Manual renewal for %s completed.", domain)
		}
	}()

	return nil
}

func (s *Server) getConfigForClient(ocspManager *OCSPManager, certMap map[string]*tls.Certificate, defaultTLSCert *tls.Certificate) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		// If this is an internal cluster request, serve the node's certificate.
		// We'll identify internal requests by checking for a specific SNI name, e.g., "node.<node-id>".
		if s.ClusterNode != nil && strings.HasPrefix(hello.ServerName, "node.") {
			s.debugLog("Serving internal cluster certificate for SNI: %s", hello.ServerName)
			nodeID := s.ClusterNode.ID
			nodePaths := tlsutil.GetNodeCertPaths(nodeID)
			cert, err := tls.LoadX509KeyPair(nodePaths.CertPath, nodePaths.KeyPath)
			if err != nil {
				s.debugLog("Failed to load node certificate for internal request: %v", err)
				// Fallback to public-facing logic
			} else {
				// For internal communication, we require client certificates, verified by our CA.
				caPaths := tlsutil.GetClusterCaPaths()
				caCertPEM, err := os.ReadFile(caPaths.CertPath)
				if err != nil {
					return nil, fmt.Errorf("could not read cluster CA for client auth: %w", err)
				}
				clientCAs := x509.NewCertPool()
				clientCAs.AppendCertsFromPEM(caCertPEM)

				// To solve the "chicken-and-egg" problem of certificate synchronization,
				// we request a client certificate but do not require it at the TLS layer.
				// This allows a new node without a trusted cert to connect to the cert
				// sync endpoints. The application-layer handler (`ServeHTTP`) is then
				// responsible for enforcing a valid client cert for all other internal APIs.
				return &tls.Config{
					Certificates: []tls.Certificate{cert},
					ClientCAs:    clientCAs,
					ClientAuth:   tls.VerifyClientCertIfGiven,
				}, nil
			}
		}

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

		// 3. Check ACME
		if s.autocertManager != nil {
			cert, err := s.autocertManager.GetCertificate(hello)
			if err != nil {
				s.debugLog("autocert.GetCertificate for %s returned error: %v, falling through", domain, err)
			} else {
				s.debugLog("Successfully obtained certificate for %s from ACME/autocert", domain)
				return &tls.Config{Certificates: []tls.Certificate{*cert}}, nil
			}
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
