package config

import (
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
		case "admin_api_key":
			if len(d.Parameters) > 0 { cfg.AdminAPIKey = d.Parameters[0] }
		case "admin_user":
			if len(d.Parameters) > 0 { cfg.AdminUser = d.Parameters[0] }
		case "admin_pass":
			if len(d.Parameters) > 0 { cfg.AdminPass = d.Parameters[0] }
		case "admin_allowlist":
			cfg.AdminAllowlist = d.Parameters
		case "log_dir":
			if len(d.Parameters) > 0 { cfg.LogDir = d.Parameters[0] }
		case "conf_dir":
			if len(d.Parameters) > 0 { cfg.ConfDir = d.Parameters[0] }
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
				case "cert_file":
					site.SSL.CertFile = d.Parameters[0]
				case "key_file":
					site.SSL.KeyFile = d.Parameters[0]
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
			if site.WAF == nil {
				site.WAF = &WAF{}
			}
			for _, d := range subBlock.Directives {
				switch d.Name {
				case "enabled":
					site.WAF.Enabled = d.Parameters[0] == "on"
				case "rules":
					site.WAF.Rules = append(site.WAF.Rules, d.Parameters...)
				}
			}
		}
	}
	return site, nil
}
