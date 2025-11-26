package config

import (
	"fmt"
	"strconv"
	"titan-web-server/internal/config/parser"
)

// DecodeAST converts a ConfigAST into a Config struct
func DecodeAST(ast *parser.ConfigAST) (*Config, error) {
	cfg := &Config{}

	// Process top-level directives
	for _, d := range ast.Directives {
		switch d.Name {
		case "http_port":
			if len(d.Parameters) > 0 { cfg.HTTPPort = d.Parameters[0] }
		case "https_port":
			if len(d.Parameters) > 0 { cfg.HTTPSPort = d.Parameters[0] }
		case "max_threads":
			if len(d.Parameters) > 0 {
				val, err := strconv.Atoi(d.Parameters[0])
				if err != nil {
					return nil, fmt.Errorf("invalid value for max_threads: not a valid integer: %s", d.Parameters[0])
				}
				cfg.MaxThreads = val
			}
		case "log_dir":
			if len(d.Parameters) > 0 { cfg.LogDir = d.Parameters[0] }
		case "geoip_db_path":
			if len(d.Parameters) > 0 { cfg.GeoIPDBPath = d.Parameters[0] }
		case "include":
			// Handle top-level includes if necessary, though usually handled by loader
		}
	}

	// Process blocks
	for _, b := range ast.Blocks {
		switch b.Name {
		case "security":
			cfg.Security = &GlobalSecurity{}
			for _, d := range b.Directives {
				if d.Name == "fips_compliant" {
					cfg.Security.FIPSCompliant = d.Parameters[0] == "on" || d.Parameters[0] == "true"
				}
			}
		case "cache":
			cfg.Cache = &Cache{}
			for _, d := range b.Directives {
				if d.Name == "type" { cfg.Cache.Type = d.Parameters[0] }
				if d.Name == "path" { cfg.Cache.Path = d.Parameters[0] }
			}
		case "server":
			site, err := decodeSiteBlock(b)
			if err != nil {
				return nil, err
			}
			cfg.Sites = append(cfg.Sites, *site)
		case "http":
			// Handle http block which can contain servers
			for _, subBlock := range b.Blocks {
				if subBlock.Name == "server" {
					site, err := decodeSiteBlock(subBlock)
					if err != nil {
						return nil, err
					}
					cfg.Sites = append(cfg.Sites, *site)
				}
			}
		}
	}

	return cfg, nil
}

func decodeSiteBlock(b parser.Block) (*Site, error) {
	site := &Site{}

	for _, d := range b.Directives {
		switch d.Name {
		case "listen":
			// In NGINX listen is complex, here we might map it to domain/port logic if needed
			// But our Site struct only has Domain currently.
			// Let's assume server_name is domain.
		case "server_name":
			if len(d.Parameters) > 0 { site.Domain = d.Parameters[0] }
		case "js_module":
			if len(d.Parameters) > 0 { site.JSModule = d.Parameters[0] }
		}
	}

	for _, subBlock := range b.Blocks {
		switch subBlock.Name {
		case "location":
			route := Route{
				Path: subBlock.Parameters[0],
			}
			// Defaults
			route.Type = "static"

			for _, d := range subBlock.Directives {
				switch d.Name {
				case "root":
					route.FileRoot = d.Parameters[0]
					route.Type = "static"
				case "proxy_pass":
					route.ProxyTarget = d.Parameters[0]
					route.Upstreams = []string{d.Parameters[0]}
					route.Type = "proxy"
				case "fastcgi_pass":
					route.ProxyTarget = d.Parameters[0] // Reusing ProxyTarget for fastcgi address
					route.Type = "php"
				case "upstream":
					// This should probably be defined outside location, but if inside, handle it
					route.Upstreams = d.Parameters
				case "js_module":
					if len(d.Parameters) > 0 {
						route.JSModule = d.Parameters[0]
					}
				case "auth_basic":
					if len(d.Parameters) > 0 {
						route.AuthBasic = d.Parameters[0]
					}
				case "auth_basic_user_file":
					if len(d.Parameters) > 0 {
						route.AuthBasicUserFile = d.Parameters[0]
					}
				case "proxy_ssl_verify":
					if len(d.Parameters) > 0 {
						route.ProxySSLVerify = d.Parameters[0] == "on"
					}
				case "limit_rate":
					if len(d.Parameters) > 0 {
						rate, err := strconv.Atoi(d.Parameters[0])
						if err == nil {
							route.LimitRate = rate
						}
					}
				}
			}

			// Handle location-specific blocks
			for _, locBlock := range subBlock.Blocks {
				switch locBlock.Name {
				case "waf":
					wafConfig, err := decodeWAFBlock(locBlock)
					if err != nil {
						return nil, fmt.Errorf("in location %s: %v", route.Path, err)
					}
					route.WAF = wafConfig
				}
			}

			site.Routes = append(site.Routes, route)

		case "ssl":
			if site.SSL == nil { site.SSL = &SSL{Enabled: true} }
			for _, d := range subBlock.Directives {
				switch d.Name {
				case "enabled":
					site.SSL.Enabled = d.Parameters[0] == "on"
				case "email":
					site.SSL.Email = d.Parameters[0]
				case "ssl_certificate":
					site.SSL.Certificate = d.Parameters[0]
				case "ssl_certificate_key":
					site.SSL.CertificateKey = d.Parameters[0]
				case "provider":
					site.SSL.Provider = d.Parameters[0]
				case "ocsp_stapling":
					site.SSL.OCSPStapling = d.Parameters[0] == "on"
				}
			}
			// Handle nested hsts block if present
			for _, sslSub := range subBlock.Blocks {
				if sslSub.Name == "hsts" {
					site.SSL.HSTS = &HSTSConfig{Enabled: true}
					for _, d := range sslSub.Directives {
						if d.Name == "max_age" {
							age, _ := strconv.Atoi(d.Parameters[0])
							site.SSL.HSTS.MaxAge = age
						}
						if d.Name == "include_subdomains" {
							site.SSL.HSTS.IncludeSubDomains = d.Parameters[0] == "on"
						}
						if d.Name == "preload" {
							site.SSL.HSTS.Preload = d.Parameters[0] == "on"
						}
					}
				}
			}
		case "waf":
			wafConfig, err := decodeWAFBlock(subBlock)
			if err != nil {
				return nil, err
			}
			site.WAF = wafConfig
		}
	}
	return site, nil
}

// decodeWAFBlock parses a WAF block from the AST.
func decodeWAFBlock(b parser.Block) (*WAFConfig, error) {
	waf := &WAFConfig{
		Enabled:   true,
		CheckSQLi: true,
		CheckXSS:  true,
		CheckLFI:  true,
		CheckUA:   true,
	}

	for _, d := range b.Directives {
		switch d.Name {
		case "enabled":
			waf.Enabled = d.Parameters[0] == "on"
		case "under_attack_mode":
			waf.UnderAttackMode = d.Parameters[0] == "on"
		case "rate_limit":
			if len(d.Parameters) > 0 {
				limit, err := strconv.Atoi(d.Parameters[0])
				if err != nil {
					return nil, fmt.Errorf("invalid value for rate_limit: %v", err)
				}
				waf.RateLimit = limit
			}
		case "allow_list":
			waf.AllowList = d.Parameters
		case "deny_list":
			waf.DenyList = d.Parameters
		case "deny_countries":
			waf.DenyCountries = d.Parameters
		case "check_sqli":
			waf.CheckSQLi = d.Parameters[0] == "on"
		case "check_xss":
			waf.CheckXSS = d.Parameters[0] == "on"
		case "check_lfi":
			waf.CheckLFI = d.Parameters[0] == "on"
		case "check_ua":
			waf.CheckUA = d.Parameters[0] == "on"
		}
	}

	for _, subBlock := range b.Blocks {
		switch subBlock.Name {
		case "js_challenge":
			waf.JSChallenge = &JSChallengeConfig{}
			// Loop through js_challenge directives if any, e.g., "enabled"
			for _, d := range subBlock.Directives {
				if d.Name == "enabled" {
					waf.JSChallenge.Enabled = d.Parameters[0] == "on"
				}
			}
		}
	}

	return waf, nil
}
