package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"titan-web-server/internal/config"
)

// ConfigManager is an interface that allows the API to get and update the server configuration
type ConfigManager interface {
	UpdateConfig(cfg *config.Config) error
	GetConfig() *config.Config
	JoinCluster(nodeID, addr string) error
	GetClusterStatus() map[string]interface{}
	RenewLetsEncrypt(domain string) error
}

// Handler handles API requests
type Handler struct {
	Manager ConfigManager
}

// NewHandler creates a new API handler
func NewHandler(manager ConfigManager) *Handler {
	return &Handler{
		Manager: manager,
	}
}

// ServeHTTP handles API requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Enable CORS for development convenience (optional, but good for separate frontends)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	path := r.URL.Path

	// Config Management
	if path == "/api/config" {
		switch r.Method {
		case http.MethodGet:
			h.handleGetConfig(w, r)
		case http.MethodPost:
			h.handlePostConfig(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Cluster Management
	if path == "/api/cluster/join" && r.Method == http.MethodPost {
		h.handleClusterJoin(w, r)
		return
	}
	if path == "/api/cluster/status" && r.Method == http.MethodGet {
		h.handleClusterStatus(w, r)
		return
	}

	// Site Management
	if path == "/api/sites" && r.Method == http.MethodPost {
		h.handleCreateSite(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/sites/") && r.Method == http.MethodDelete {
		domain := strings.TrimPrefix(path, "/api/sites/")
		h.handleDeleteSite(w, r, domain)
		return
	}

	// SSL Management
	if path == "/api/ssl/upload" && r.Method == http.MethodPost {
		h.handleUploadSSL(w, r)
		return
	}
	if path == "/api/ssl/toggle" && r.Method == http.MethodPost {
		h.handleToggleSSL(w, r)
		return
	}
	if path == "/api/letsencrypt/renew" && r.Method == http.MethodPost {
		h.handleRenewLetsEncrypt(w, r)
		return
	}

	http.NotFound(w, r)
}

func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.Manager.GetConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (h *Handler) handlePostConfig(w http.ResponseWriter, r *http.Request) {
	var newConfig config.Config
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&newConfig); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.Manager.UpdateConfig(&newConfig); err != nil {
		http.Error(w, "Failed to update config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Configuration updated successfully"))
}

func (h *Handler) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Addr string `json:"addr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.Manager.JoinCluster(req.ID, req.Addr); err != nil {
		http.Error(w, "Failed to join cluster: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ok"}`))
}

func (h *Handler) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	status := h.Manager.GetClusterStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain      string `json:"domain"`
		Type        string `json:"type"` // "proxy", "static", "php"
		Target      string `json:"target"` // ProxyTarget or FileRoot
		Port        int    `json:"port"` // Optional upstream port
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Domain == "" {
		http.Error(w, "Domain is required", http.StatusBadRequest)
		return
	}

	cfg := h.Manager.GetConfig()

	// Check if site exists
	for _, site := range cfg.Sites {
		if site.Domain == req.Domain {
			http.Error(w, "Site already exists", http.StatusConflict)
			return
		}
	}

	newSite := config.Site{
		Domain: req.Domain,
		Routes: []config.Route{},
		SSL: &config.SSL{
			Enabled: false, // Default to disabled, user can enable later
		},
	}

	// Create default route /
	route := config.Route{
		Path: "/",
		Type: req.Type,
	}

	if req.Type == "proxy" {
		route.ProxyTarget = req.Target
		// Also set Upstreams for consistency
		route.Upstreams = []string{req.Target}
	} else if req.Type == "static" || req.Type == "php" {
		route.FileRoot = req.Target
		if req.Type == "php" {
			// Default PHP target
			route.ProxyTarget = "127.0.0.1:9000"
		}
	} else {
		http.Error(w, "Invalid type", http.StatusBadRequest)
		return
	}

	newSite.Routes = append(newSite.Routes, route)
	cfg.Sites = append(cfg.Sites, newSite)

	if err := h.Manager.UpdateConfig(cfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "created"}`))
}

func (h *Handler) handleDeleteSite(w http.ResponseWriter, r *http.Request, domain string) {
	cfg := h.Manager.GetConfig()
	newSites := []config.Site{}
	found := false

	for _, site := range cfg.Sites {
		if site.Domain != domain {
			newSites = append(newSites, site)
		} else {
			found = true
		}
	}

	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	cfg.Sites = newSites
	if err := h.Manager.UpdateConfig(cfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "deleted"}`))
}

func (h *Handler) handleUploadSSL(w http.ResponseWriter, r *http.Request) {
	// Multipart form upload
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	domain := r.FormValue("domain")
	if domain == "" {
		http.Error(w, "Domain required", http.StatusBadRequest)
		return
	}

	// Create directory
	certDir := "/etc/titan/certs/manual"
	if err := os.MkdirAll(certDir, 0700); err != nil {
		http.Error(w, "Failed to create cert dir", http.StatusInternalServerError)
		return
	}

	// Handle Cert
	certFile, _, err := r.FormFile("cert_file")
	if err != nil {
		http.Error(w, "cert_file required", http.StatusBadRequest)
		return
	}
	defer certFile.Close()

	// Handle Key
	keyFile, _, err := r.FormFile("key_file")
	if err != nil {
		http.Error(w, "key_file required", http.StatusBadRequest)
		return
	}
	defer keyFile.Close()

	// Save files
	certPath := filepath.Join(certDir, domain+".crt")
	keyPath := filepath.Join(certDir, domain+".key")

	dstCert, err := os.Create(certPath)
	if err != nil {
		http.Error(w, "Failed to save cert", http.StatusInternalServerError)
		return
	}
	defer dstCert.Close()
	io.Copy(dstCert, certFile)

	dstKey, err := os.Create(keyPath)
	if err != nil {
		http.Error(w, "Failed to save key", http.StatusInternalServerError)
		return
	}
	defer dstKey.Close()
	io.Copy(dstKey, keyFile)

	// Update Config
	cfg := h.Manager.GetConfig()
	found := false
	for i, site := range cfg.Sites {
		if site.Domain == domain {
			if cfg.Sites[i].SSL == nil {
				cfg.Sites[i].SSL = &config.SSL{}
			}
			cfg.Sites[i].SSL.Enabled = true
			cfg.Sites[i].SSL.Provider = "manual"
			cfg.Sites[i].SSL.CertFile = certPath
			cfg.Sites[i].SSL.KeyFile = keyPath
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Domain not found in config", http.StatusNotFound)
		return
	}

	if err := h.Manager.UpdateConfig(cfg); err != nil {
		http.Error(w, "Failed to update config", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "uploaded"}`))
}

func (h *Handler) handleToggleSSL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain   string `json:"domain"`
		Provider string `json:"provider"` // "letsencrypt" or "off"
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	cfg := h.Manager.GetConfig()
	found := false
	for i, site := range cfg.Sites {
		if site.Domain == req.Domain {
			if cfg.Sites[i].SSL == nil {
				cfg.Sites[i].SSL = &config.SSL{}
			}

			if req.Provider == "off" {
				cfg.Sites[i].SSL.Enabled = false
			} else {
				cfg.Sites[i].SSL.Enabled = true
				cfg.Sites[i].SSL.Provider = req.Provider
				if req.Email != "" {
					cfg.Sites[i].SSL.Email = req.Email
				}
			}
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	if err := h.Manager.UpdateConfig(cfg); err != nil {
		http.Error(w, "Failed to update config", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "updated"}`))
}

func (h *Handler) handleRenewLetsEncrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Domain == "" {
		http.Error(w, "Domain is required", http.StatusBadRequest)
		return
	}

	if err := h.Manager.RenewLetsEncrypt(req.Domain); err != nil {
		http.Error(w, "Failed to renew certificate: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "renewal-triggered"}`))
}
