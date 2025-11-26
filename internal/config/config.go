package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"titan-web-server/internal/config/parser"
)

// Config represents the global configuration
type Config struct {
	HTTPPort       string          `json:"http_port"`        // e.g. ":80"
	HTTPSPort      string          `json:"https_port"`       // e.g. ":443"
	MaxThreads     int             `json:"max_threads"`      // Max number of OS threads
	AdminAPIKey    string          `json:"admin_api_key"`    // API Key for management
	AdminUser      string          `json:"admin_user"`       // Username for login
	AdminPass      string          `json:"admin_pass"`       // Password for login (plaintext for MVP)
	AdminAllowlist []string        `json:"admin_allowlist"`  // IPs allowed to access admin API
	LogDir         string          `json:"log_dir"`          // Directory for log files (default: /var/log/titan/)
	ConfDir        string          `json:"conf_dir"`         // Directory for additional site configs (e.g. "conf/sites.d")
	GeoIPDBPath    string          `json:"geoip_db_path"`    // Path to GeoIP MMDB file
	Cache          *Cache          `json:"cache"`            // Cache configuration
	Security       *GlobalSecurity `json:"security"`         // Global security settings
	Sites          []Site          `json:"sites"`
	ManualCerts    map[string]*tls.Certificate `json:"-"` // Loaded from /etc/titan/certs/manual
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
			if route.Type == "proxy" && route.ProxyTarget == "" && len(route.Upstreams) == 0 {
				return fmt.Errorf("site %s route %s missing proxy_target or upstreams", site.Domain, route.Path)
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
	Domain string      `json:"domain"`
	Routes []Route     `json:"routes"`
	SSL    *SSL        `json:"ssl,omitempty"`
	WAF    *WAFConfig  `json:"waf,omitempty"`
}

// WAFConfig represents the Web Application Firewall configuration for a site
type WAFConfig struct {
	Enabled         bool               `json:"enabled"`
	UnderAttackMode bool               `json:"under_attack_mode"`
	RateLimit       int                `json:"rate_limit"`       // Requests per minute per IP
	AllowList       []string           `json:"allow_list"`       // IP whitelist
	DenyList        []string           `json:"deny_list"`        // IP blacklist
	DenyCountries   []string           `json:"deny_countries"`   // Block traffic from these country codes (ISO 3166-1 alpha-2)
	CheckSQLi       bool               `json:"check_sqli"`
	CheckXSS        bool               `json:"check_xss"`
	CheckLFI        bool               `json:"check_lfi"`
	CheckUA         bool               `json:"check_ua"`
	JSChallenge     *JSChallengeConfig `json:"js_challenge,omitempty"`
}

// JSChallengeConfig represents the JS Challenge configuration
type JSChallengeConfig struct {
	Enabled bool `json:"enabled"`
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
	Path        string     `json:"path"`                  // e.g., "/" or "/api"
	Type        string     `json:"type"`                  // "proxy" or "static"
	ProxyTarget string     `json:"proxy_target,omitempty"`// Single target (legacy/simple)
	Upstreams   []string   `json:"upstreams,omitempty"`   // Load balancing targets
	FileRoot    string     `json:"file_root"`             // e.g., "/var/www/html"
	WAF         *WAFConfig `json:"waf,omitempty"`
}

// LoadConfig loads configuration from a .conf file and optional include directory
func LoadConfig(filename string) (*Config, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	p := parser.NewParser(string(content))
	ast, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse error in %s: %v", filename, err)
	}

	cfg, err := DecodeAST(ast)
	if err != nil {
		return nil, fmt.Errorf("decode error in %s: %v", filename, err)
	}

	// Normalize routes: migrate ProxyTarget to Upstreams if empty
	for i := range cfg.Sites {
		for j := range cfg.Sites[i].Routes {
			route := &cfg.Sites[i].Routes[j]
			if route.ProxyTarget != "" && len(route.Upstreams) == 0 {
				route.Upstreams = []string{route.ProxyTarget}
			}
		}
	}

	// Load additional configs if ConfDir is set
	if cfg.ConfDir != "" {
		// Look for .conf files
		matches, err := filepath.Glob(filepath.Join(cfg.ConfDir, "*.conf"))
		if err == nil {
			for _, match := range matches {
				siteCfg, err := loadSiteConfig(match)
				if err != nil {
					fmt.Printf("Warning: failed to load config from %s: %v\n", match, err)
					continue
				}
				cfg.Sites = append(cfg.Sites, siteCfg...)
			}
		}
	}

	return cfg, nil
}

func loadSiteConfig(filename string) ([]Site, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	p := parser.NewParser(string(content))
	ast, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse error in %s: %v", filename, err)
	}

	// We expect sites to be in "server" blocks, possibly inside "http", or at root.
	// But our DecodeAST handles top level "server" and "http" blocks.
	// So we can reuse DecodeAST but we need to extract sites from the resulting cfg.

	tempCfg, err := DecodeAST(ast)
	if err != nil {
		return nil, err
	}

	return tempCfg.Sites, nil
}
