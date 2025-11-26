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
	HTTPPort    string
	HTTPSPort   string
	MaxThreads  int
	LogDir      string
	GeoIPDBPath string
	Cache       *Cache
	Security    *GlobalSecurity
	Sites       []Site
	ManualCerts map[string]*tls.Certificate
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
	FIPSCompliant bool
}

// Cache configuration
type Cache struct {
	Type string
	Path string
}

// Site represents a virtual host
type Site struct {
	Domain   string
	Routes   []Route
	SSL      *SSL
	WAF      *WAFConfig
	JSModule string
}

// WAFConfig represents the Web Application Firewall configuration for a site
type WAFConfig struct {
	Enabled         bool
	UnderAttackMode bool
	RateLimit       int
	AllowList       []string
	DenyList        []string
	DenyCountries   []string
	CheckSQLi       bool
	CheckXSS        bool
	CheckLFI        bool
	CheckUA         bool
	JSChallenge     *JSChallengeConfig
}

// JSChallengeConfig represents the JS Challenge configuration
type JSChallengeConfig struct {
	Enabled bool
}

// SSL represents SSL configuration
type SSL struct {
	Enabled      bool
	Email        string
	CertFile     string
	KeyFile      string
	Provider     string
	HSTS         *HSTSConfig
	OCSPStapling bool
}

// HSTSConfig represents HTTP Strict Transport Security settings
type HSTSConfig struct {
	Enabled           bool
	MaxAge            int
	IncludeSubDomains bool
	Preload           bool
}

// Route represents a path rule
type Route struct {
	Path              string
	Type              string
	ProxyTarget       string
	Upstreams         []string
	FileRoot          string
	WAF               *WAFConfig
	JSModule          string
	AuthBasic         string
	AuthBasicUserFile string
	ProxySSLVerify    bool
	LimitRate         int
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

	// Load additional configs from conf.d
	matches, err := filepath.Glob("conf.d/*.conf")
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

	// Load additional configs from sites-enabled
	matches, err = filepath.Glob("sites-enabled/*")
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
