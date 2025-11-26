package config

import (
	"fmt"
	"strings"
)

// EncodeToConf converts a Config struct back to NGINX-like configuration string
func EncodeToConf(c *Config) string {
	var sb strings.Builder

	if c.HTTPPort != "" {
		sb.WriteString(fmt.Sprintf("http_port %s;\n", c.HTTPPort))
	}
	if c.HTTPSPort != "" {
		sb.WriteString(fmt.Sprintf("https_port %s;\n", c.HTTPSPort))
	}

	if c.AdminAPIKey != "" {
		sb.WriteString(fmt.Sprintf("admin_api_key \"%s\";\n", c.AdminAPIKey))
	}
	if c.AdminUser != "" {
		sb.WriteString(fmt.Sprintf("admin_user \"%s\";\n", c.AdminUser))
	}
	if c.AdminPass != "" {
		sb.WriteString(fmt.Sprintf("admin_pass \"%s\";\n", c.AdminPass))
	}
	if len(c.AdminAllowlist) > 0 {
		sb.WriteString("admin_allowlist")
		for _, ip := range c.AdminAllowlist {
			sb.WriteString(" " + ip)
		}
		sb.WriteString(";\n")
	}

	if c.LogDir != "" {
		sb.WriteString(fmt.Sprintf("log_dir \"%s\";\n", c.LogDir))
	}
	if c.ConfDir != "" {
		sb.WriteString(fmt.Sprintf("conf_dir \"%s\";\n", c.ConfDir))
	}

	if c.Security != nil {
		sb.WriteString("security {\n")
		if c.Security.FIPSCompliant {
			sb.WriteString("    fips_compliant on;\n")
		} else {
			sb.WriteString("    fips_compliant off;\n")
		}
		sb.WriteString("}\n")
	}

	if c.Cache != nil {
		sb.WriteString("cache {\n")
		sb.WriteString(fmt.Sprintf("    type %s;\n", c.Cache.Type))
		if c.Cache.Path != "" {
			sb.WriteString(fmt.Sprintf("    path \"%s\";\n", c.Cache.Path))
		}
		sb.WriteString("}\n")
	}

	sb.WriteString("\nhttp {\n")
	for _, site := range c.Sites {
		sb.WriteString("    server {\n")
		sb.WriteString(fmt.Sprintf("        server_name %s;\n", site.Domain))

		if site.SSL != nil && site.SSL.Enabled {
			sb.WriteString("        ssl {\n")
			sb.WriteString("            enabled on;\n")
			if site.SSL.Email != "" {
				sb.WriteString(fmt.Sprintf("            email \"%s\";\n", site.SSL.Email))
			}
			if site.SSL.CertFile != "" {
				sb.WriteString(fmt.Sprintf("            cert_file \"%s\";\n", site.SSL.CertFile))
			}
			if site.SSL.KeyFile != "" {
				sb.WriteString(fmt.Sprintf("            key_file \"%s\";\n", site.SSL.KeyFile))
			}
			if site.SSL.Provider != "" {
				sb.WriteString(fmt.Sprintf("            provider %s;\n", site.SSL.Provider))
			}
			if site.SSL.OCSPStapling {
				sb.WriteString("            ocsp_stapling on;\n")
			}

			if site.SSL.HSTS != nil && site.SSL.HSTS.Enabled {
				sb.WriteString("            hsts {\n")
				sb.WriteString(fmt.Sprintf("                max_age %d;\n", site.SSL.HSTS.MaxAge))
				if site.SSL.HSTS.IncludeSubDomains {
					sb.WriteString("                include_subdomains on;\n")
				}
				if site.SSL.HSTS.Preload {
					sb.WriteString("                preload on;\n")
				}
				sb.WriteString("            }\n")
			}
			sb.WriteString("        }\n")
		}

		for _, route := range site.Routes {
			sb.WriteString(fmt.Sprintf("        location %s {\n", route.Path))
			if route.FileRoot != "" {
				sb.WriteString(fmt.Sprintf("            root \"%s\";\n", route.FileRoot))
			}

			if route.Type == "proxy" {
				if len(route.Upstreams) > 0 {
					// Use first one for proxy_pass, or support upstream block?
					// For simplicity, just use proxy_pass for first.
					sb.WriteString(fmt.Sprintf("            proxy_pass %s;\n", route.Upstreams[0]))
				} else if route.ProxyTarget != "" {
					sb.WriteString(fmt.Sprintf("            proxy_pass %s;\n", route.ProxyTarget))
				}
			} else if route.Type == "php" {
				if route.ProxyTarget != "" {
					sb.WriteString(fmt.Sprintf("            fastcgi_pass %s;\n", route.ProxyTarget))
				}
			}

			if route.WAF != nil {
				sb.WriteString(encodeWAFBlock(route.WAF, "            "))
			}
			sb.WriteString("        }\n")
		}

		if site.WAF != nil {
			sb.WriteString(encodeWAFBlock(site.WAF, "        "))
		}

		sb.WriteString("    }\n")
	}
	sb.WriteString("}\n")

	return sb.String()
}

// encodeWAFBlock generates the string representation for a WAFConfig
func encodeWAFBlock(w *WAFConfig, indent string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%swaf {\n", indent))
	innerIndent := indent + "    "

	// Note: We write the config explicitly rather than relying on defaults
	sb.WriteString(fmt.Sprintf("%senabled %s;\n", innerIndent, boolToOnOff(w.Enabled)))
	sb.WriteString(fmt.Sprintf("%sunder_attack_mode %s;\n", innerIndent, boolToOnOff(w.UnderAttackMode)))

	if w.RateLimit > 0 {
		sb.WriteString(fmt.Sprintf("%srate_limit %d;\n", innerIndent, w.RateLimit))
	}

	if len(w.AllowList) > 0 {
		sb.WriteString(fmt.Sprintf("%sallow_list %s;\n", innerIndent, strings.Join(w.AllowList, " ")))
	}
	if len(w.DenyList) > 0 {
		sb.WriteString(fmt.Sprintf("%sdeny_list %s;\n", innerIndent, strings.Join(w.DenyList, " ")))
	}
	if len(w.DenyCountries) > 0 {
		sb.WriteString(fmt.Sprintf("%sdeny_countries %s;\n", innerIndent, strings.Join(w.DenyCountries, " ")))
	}

	sb.WriteString(fmt.Sprintf("%scheck_sqli %s;\n", innerIndent, boolToOnOff(w.CheckSQLi)))
	sb.WriteString(fmt.Sprintf("%scheck_xss %s;\n", innerIndent, boolToOnOff(w.CheckXSS)))
	sb.WriteString(fmt.Sprintf("%scheck_lfi %s;\n", innerIndent, boolToOnOff(w.CheckLFI)))
	sb.WriteString(fmt.Sprintf("%scheck_ua %s;\n", innerIndent, boolToOnOff(w.CheckUA)))

	if w.JSChallenge != nil {
		sb.WriteString(fmt.Sprintf("%sjs_challenge {\n", innerIndent))
		sb.WriteString(fmt.Sprintf("%s    enabled %s;\n", innerIndent, boolToOnOff(w.JSChallenge.Enabled)))
		sb.WriteString(fmt.Sprintf("%s}\n", innerIndent))
	}

	sb.WriteString(fmt.Sprintf("%s}\n", indent))
	return sb.String()
}

func boolToOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
