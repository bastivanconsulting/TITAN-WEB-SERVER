package server

import (
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

	"github.com/fsnotify/fsnotify"
	"github.com/quic-go/quic-go/http3"
	"github.com/yookoala/gofast"

	"custom-proxy/internal/api"
	"custom-proxy/internal/auth"
	"custom-proxy/internal/cache"
	"custom-proxy/internal/config"
	"custom-proxy/internal/proxy"
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
	phpHandlers   map[string]http.Handler
	proxyHandlers map[string]http.Handler

	// Security / TLS state
	certMap     map[string]*tls.Certificate
	ocspManager *OCSPManager
	tlsConfig   *tls.Config
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

	srv := &Server{
		Config:        cfg,
		ConfigPath:    configPath,
		Tpl:           tpl,
		Cache:         c,
		phpHandlers:   make(map[string]http.Handler),
		proxyHandlers: make(map[string]http.Handler),
		certMap:       make(map[string]*tls.Certificate),
		ocspManager:   NewOCSPManager(),
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

	// Initialize handlers (this also sorts routes and sets up TLS)
	if err := srv.UpdateConfig(cfg); err != nil {
		panic(fmt.Sprintf("Failed to initialize configuration: %v", err))
	}

	return srv
}

// GetConfig returns a copy of the current configuration (thread-safe)
func (s *Server) GetConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}

// UpdateConfig updates the server configuration thread-safely
func (s *Server) UpdateConfig(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update Auth Provider credentials if changed
	if cfg.AdminUser != "" || cfg.AdminPass != "" {
		user := cfg.AdminUser
		pass := cfg.AdminPass
		if user == "" { user = "admin" }
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

	// Update TLS State (Certs, FIPS config, OCSP)
	// This ensures that when config is reloaded, TLS settings are updated
	if err := s.updateTLSStateLocked(); err != nil {
		fmt.Printf("Error updating TLS state: %v\n", err)
		// We continue to update routes even if TLS update fails partialy
	}

	// Re-initialize handlers
	if err := s.initHandlersLocked(); err != nil {
		return err
	}

	// Persist configuration to disk
	return s.saveConfigLocked()
}

func (s *Server) updateTLSStateLocked() error {
	// Rebuild certificate map
	newCertMap := make(map[string]*tls.Certificate)
	// We might need to restart OCSP manager if certificates change significantly,
	// but for now we just add to it.
	// Ideally we should prune old certs from OCSP manager, but for MVP just adding new ones is safer than race conditions.

	for _, site := range s.Config.Sites {
		if site.SSL != nil && site.SSL.Enabled {
			if site.SSL.Provider == "manual" {
				cert, err := tls.LoadX509KeyPair(site.SSL.CertFile, site.SSL.KeyFile)
				if err != nil {
					fmt.Printf("Error loading cert for %s: %v\n", site.Domain, err)
					continue
				}

				domain := strings.ToLower(site.Domain)
				newCertMap[domain] = &cert

				if site.SSL.OCSPStapling {
					s.ocspManager.AddCertificate(domain, &cert)
				}
			}
		}
	}
	s.certMap = newCertMap

	// Rebuild TLS Config
	var tlsConfig *tls.Config
	if s.Config.Security != nil && s.Config.Security.FIPSCompliant {
		tlsConfig = getFIPSTLSConfig(s.Config.Security.FIPSStandard)
	} else {
		tlsConfig = &tls.Config{}
	}

	// Set the GetCertificate callback to use the *Server* instance's map
	// Note: We use a closure that locks the server to read the map safely
	// However, GetCertificate is called during handshake, so locking s.mu (RWMutex) is fine and fast.
	tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		domain := strings.ToLower(hello.ServerName)
		if cert, ok := s.certMap[domain]; ok {
			if staple := s.ocspManager.GetStaple(domain); staple != nil {
				certWithStaple := *cert
				certWithStaple.OCSPStaple = staple
				return &certWithStaple, nil
			}
			return cert, nil
		}
		return nil, errors.New("certificate not found for domain")
	}
	tlsConfig.NextProtos = []string{"h3", "http/1.1"}

	s.tlsConfig = tlsConfig
	return nil
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
	// Add global Server header
	w.Header().Set("Server", "Titan Web Server")

	// 0. Intercept API and Admin requests
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPI(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin") {
		s.handleAdmin(w, r)
		return
	}
	if r.URL.Path == "/login" && r.Method == http.MethodPost {
		s.handleLogin(w, r)
		return
	}

	// 1. Match Host
	host := r.Host
	// Remove port if present
	if strings.Contains(host, ":") {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			if strings.Contains(r.Host, ":") {
				host = strings.Split(r.Host, ":")[0]
			}
		}
	}

	// Read config safely
	s.mu.RLock()
	cfg := s.Config
	s.mu.RUnlock()

	var matchedSite *config.Site
	for _, site := range cfg.Sites {
		if site.Domain == host {
			matchedSite = &site
			break
		}
	}

	if matchedSite == nil {
		s.renderError(w, r, 404, "Site non trouvé", "Le domaine demandé n'est pas configuré sur ce serveur.")
		return
	}

	// HSTS Handling
	// Ensure HSTS is only sent over secure connections (TLS)
	if r.TLS != nil && matchedSite.SSL != nil && matchedSite.SSL.Enabled && matchedSite.SSL.HSTS != nil && matchedSite.SSL.HSTS.Enabled {
		hsts := matchedSite.SSL.HSTS
		val := fmt.Sprintf("max-age=%d", hsts.MaxAge)
		if hsts.IncludeSubDomains {
			val += "; includeSubDomains"
		}
		if hsts.Preload {
			val += "; preload"
		}
		w.Header().Set("Strict-Transport-Security", val)
	}

	// 2. Match Route (Longest prefix match is usually best, but simple first match for now)
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
			return
		}
		proxyHandler.ServeHTTP(w, r)
	case "static":
		fs := http.FileServer(http.Dir(matchedRoute.FileRoot))
		http.StripPrefix(matchedRoute.Path, fs).ServeHTTP(w, r)
	case "php":
		handler := s.getPHPHandler(matchedRoute)
		if handler != nil {
			handler.ServeHTTP(w, r)
		} else {
			s.renderError(w, r, 500, "Erreur Configuration", "Handler PHP non initialisé")
		}

	default:
		s.renderError(w, r, 500, "Erreur Configuration", "Type de route invalide.")
	}
}

// WatchConfig monitors the config file for changes and reloads it
func (s *Server) WatchConfig() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Error creating config watcher: %v\n", err)
		return
	}

	// Start listening for events.
	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					fmt.Println("Config file modified. Reloading...")
					// Small delay to ensure write is complete
					time.Sleep(100 * time.Millisecond)

					newCfg, err := config.LoadConfig(s.ConfigPath)
					if err != nil {
						fmt.Printf("Error reloading config: %v\n", err)
						continue
					}

					if err := s.UpdateConfig(newCfg); err != nil {
						fmt.Printf("Error applying new config: %v\n", err)
					} else {
						fmt.Println("Configuration reloaded successfully (including TLS settings).")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("Config watcher error: %v\n", err)
			}
		}
	}()

	// Watch the file specifically
	if s.ConfigPath != "" {
		err = watcher.Add(s.ConfigPath)
	} else {
		err = watcher.Add("config.json")
	}

	if err != nil {
		fmt.Printf("Error adding config file to watcher: %v\n", err)
	} else {
		fmt.Printf("Watching configuration file for changes: %s\n", s.ConfigPath)
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Start Config Watcher
	s.WatchConfig()
	// Start OCSP Manager background process
	s.ocspManager.Start()

	// Error channel to catch failures from goroutines
	errChan := make(chan error)

	// Start HTTP Server if configured
	if s.Config.HTTPPort != "" {
		go func() {
			fmt.Printf("Starting HTTP server on %s\n", s.Config.HTTPPort)
			if err := http.ListenAndServe(s.Config.HTTPPort, s); err != nil {
				errChan <- fmt.Errorf("HTTP server failed: %w", err)
			}
		}()
	}

	// Start HTTPS Server if configured
	if s.Config.HTTPSPort != "" {
		go func() {
			// We use a dynamic TLS config wrapper that delegates to s.tlsConfig
			// This allows us to swap the config under the hood if needed,
			// though GetCertificate wrapper above already handles dynamic certs.
			// The critical part is that s.tlsConfig itself holds the FIPS cipher suites.
			// Ideally http.Server reads TLSConfig on every connection? No, it caches it?
			// `crypto/tls` docs say: "The Config structure ... should not be modified after it is used."
			// However, `GetCertificate` is a callback.
			// But `CipherSuites` are fixed in the config struct passed to ListenAndServe.
			// To support changing FIPS mode (Ciphers) dynamically, we need `GetConfigForClient`.

			// Let's implement GetConfigForClient to return the full config snapshot for each connection.
			// This is the robust way to handle hot-reloading of cipher suites/versions.

			masterTLSConfig := &tls.Config{
				GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					s.mu.RLock()
					defer s.mu.RUnlock()
					// Return the current fully configured TLS config
					// We must return a clone or the pointer if we treat it as immutable per generation.
					// s.tlsConfig is recreated in updateTLSStateLocked, so it's safe to return that pointer
					// as long as we don't modify it in place.
					return s.tlsConfig, nil
				},
				NextProtos: []string{"h3", "http/1.1"},
			}

			// Start HTTP/3 (QUIC) Server
			quicServer := &http3.Server{
				Addr:      s.Config.HTTPSPort,
				Handler:   s,
				TLSConfig: masterTLSConfig,
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
					port := s.Config.HTTPSPort
					if strings.HasPrefix(port, ":") {
						port = port[1:]
					}
					w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%s"; ma=2592000`, port))
					s.ServeHTTP(w, r)
				}),
				TLSConfig: masterTLSConfig,
			}

			fmt.Printf("Starting HTTPS server on %s (SNI Enabled)\n", s.Config.HTTPSPort)
			if err := server.ListenAndServeTLS("", ""); err != nil {
				errChan <- fmt.Errorf("HTTPS server failed: %w", err)
			}
		}()
	}

	return <-errChan
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	allowlist := s.Config.AdminAllowlist
	apiKey := s.Config.AdminAPIKey
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
			http.Error(w, "Forbidden (IP)", http.StatusForbidden)
			return
		}
	}

	keyHeader := r.Header.Get("X-API-Key")
	if keyHeader != apiKey {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "Forbidden", "message": "Invalid or missing API Key"}`))
		return
	}

	s.APIHandler.ServeHTTP(w, r)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
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
		s.mu.RLock()
		apiKey := s.Config.AdminAPIKey
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success": true, "api_key": "%s"}`, apiKey)
		return
	}

	http.Error(w, "Invalid credentials", http.StatusUnauthorized)
}
