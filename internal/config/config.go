package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Config represents the global configuration
type Config struct {
	HTTPPort       string          `json:"http_port"`        // e.g. ":80"
	HTTPSPort      string          `json:"https_port"`       // e.g. ":443"
	AdminAPIKey    string          `json:"admin_api_key"`    // API Key for management
	AdminUser      string          `json:"admin_user"`       // Username for login
	AdminPass      string          `json:"admin_pass"`       // Password for login (plaintext for MVP)
	AdminAllowlist []string        `json:"admin_allowlist"`  // IPs allowed to access admin API
	LogDir         string          `json:"log_dir"`          // Directory for log files (default: /var/log/titan/)
	Cache          *Cache          `json:"cache"`            // Cache configuration
	Security       *GlobalSecurity `json:"security"`         // Global security settings
	Sites          []Site          `json:"sites"`
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	if c.HTTPPort == "" && c.HTTPSPort == "" {
		return errors.New("at least one of http_port or https_port must be set")
	}

	for i, site := range c.Sites {
		if site.Domain == "" {
			return fmt.Errorf("site #%d missing domain", i)
		}
		for j, route := range site.Routes {
			if route.Path == "" {
				return fmt.Errorf("site %s route #%d missing path", site.Domain, j)
			}
			if route.Type == "proxy" && route.ProxyTarget == "" {
				return fmt.Errorf("site %s route %s missing proxy_target", site.Domain, route.Path)
			}
			if (route.Type == "static" || route.Type == "php") && route.FileRoot == "" {
				return fmt.Errorf("site %s route %s missing file_root", site.Domain, route.Path)
			}
		}
	}
	return nil
}

// GlobalSecurity holds global security settings
type GlobalSecurity struct {
	FIPSCompliant bool `json:"fips_compliant"` // Enforce FIPS 140-2/3 compliant TLS settings
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
	Enabled      bool        `json:"enabled"`
	Email        string      `json:"email"`         // For ACME registration
	CertFile     string      `json:"cert_file"`     // Manual cert
	KeyFile      string      `json:"key_file"`      // Manual key
	Provider     string      `json:"provider"`      // "letsencrypt", "selfsigned", "manual"
	HSTS         *HSTSConfig `json:"hsts,omitempty"` // HSTS Configuration
	OCSPStapling bool        `json:"ocsp_stapling"`  // Enable OCSP Stapling
}

// HSTSConfig represents HTTP Strict Transport Security settings
type HSTSConfig struct {
	Enabled           bool `json:"enabled"`
	MaxAge            int  `json:"max_age"` // Seconds
	IncludeSubDomains bool `json:"include_subdomains"`
	Preload           bool `json:"preload"`
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
