package config

import (
	"encoding/json"
	"os"
)

// Config represents the global configuration
type Config struct {
	HTTPPort       string   `json:"http_port"`        // e.g. ":80"
	HTTPSPort      string   `json:"https_port"`       // e.g. ":443"
	AdminAPIKey    string   `json:"admin_api_key"`    // API Key for management
	AdminUser      string   `json:"admin_user"`       // Username for login
	AdminPass      string   `json:"admin_pass"`       // Password for login (plaintext for MVP)
	AdminAllowlist []string `json:"admin_allowlist"`  // IPs allowed to access admin API
	Cache          *Cache   `json:"cache"`            // Cache configuration
	Sites          []Site   `json:"sites"`
}

// Cache configuration
type Cache struct {
	Type string `json:"type"` // "memory" or "disk"
	Path string `json:"path"` // Path for disk cache
}

// Site represents a virtual host
type Site struct {
	Domain string  `json:"domain"`
	Routes []Route `json:"routes"`
	SSL    *SSL    `json:"ssl,omitempty"`
}

// SSL represents SSL configuration
type SSL struct {
	Enabled      bool   `json:"enabled"`
	Email        string `json:"email"`        // For ACME registration
	CertFile     string `json:"cert_file"`    // Manual cert
	KeyFile      string `json:"key_file"`     // Manual key
	Provider     string `json:"provider"`     // "letsencrypt", "selfsigned", "manual"
}

// Route represents a path rule
type Route struct {
	Path        string `json:"path"`         // e.g., "/" or "/api"
	Type        string `json:"type"`         // "proxy" or "static"
	ProxyTarget string `json:"proxy_target"` // e.g., "http://localhost:8080"
	FileRoot    string `json:"file_root"`    // e.g., "/var/www/html"
}

// LoadConfig loads configuration from a JSON file
func LoadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &Config{}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
