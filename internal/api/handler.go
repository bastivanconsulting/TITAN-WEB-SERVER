package api

import (
	"encoding/json"
	"net/http"

	"custom-proxy/internal/config"
)

// ConfigManager is an interface that allows the API to get and update the server configuration
type ConfigManager interface {
	UpdateConfig(cfg *config.Config) error
	GetConfig() *config.Config
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

// ServeHTTP handles /api/config requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/config" {
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
