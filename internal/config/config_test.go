package config

import (
	"strings"
	"testing"
	"titan-web-server/internal/config/parser"
)

func TestEncodeToConf(t *testing.T) {
	cfg := &Config{
		HTTPPort: ":8080",
		Sites: []Site{
			{
				Domain: "example.com",
				Routes: []Route{
					{
						Path: "/",
						FileRoot: "/var/www",
						Type: "static",
					},
				},
			},
		},
	}

	encoded := EncodeToConf(cfg)
	if !strings.Contains(encoded, "http_port :8080;") {
		t.Error("Encoded config missing http_port")
	}
	if !strings.Contains(encoded, "server_name example.com;") {
		t.Error("Encoded config missing server_name")
	}
}

func TestRoundTrip(t *testing.T) {
	original := &Config{
		HTTPPort: ":9090",
		AdminUser: "admin",
		Sites: []Site{
			{
				Domain: "test.local",
				Routes: []Route{
					{
						Path: "/api",
						ProxyTarget: "http://backend",
						Type: "proxy",
						Upstreams: []string{"http://backend"},
					},
				},
			},
		},
	}

	encoded := EncodeToConf(original)

	p := parser.NewParser(encoded)
	ast, err := p.Parse()
	if err != nil {
		t.Fatalf("Failed to parse encoded config: %v\nConfig:\n%s", err, encoded)
	}

	decoded, err := DecodeAST(ast)
	if err != nil {
		t.Fatalf("Failed to decode AST: %v", err)
	}

	if decoded.HTTPPort != original.HTTPPort {
		t.Errorf("HTTPPort mismatch: got %s, want %s", decoded.HTTPPort, original.HTTPPort)
	}
	if decoded.AdminUser != original.AdminUser {
		t.Errorf("AdminUser mismatch: got %s, want %s", decoded.AdminUser, original.AdminUser)
	}
	if len(decoded.Sites) != 1 {
		t.Fatalf("Expected 1 site, got %d", len(decoded.Sites))
	}
	if decoded.Sites[0].Domain != original.Sites[0].Domain {
		t.Errorf("Domain mismatch: got %s, want %s", decoded.Sites[0].Domain, original.Sites[0].Domain)
	}
	if len(decoded.Sites[0].Routes) != 1 {
		t.Fatalf("Expected 1 route")
	}
	// Note: EncodeToConf populates proxy_pass from Upstreams[0] if present.
	// DecodeAST populates ProxyTarget and Upstreams from proxy_pass.
	if decoded.Sites[0].Routes[0].Path != original.Sites[0].Routes[0].Path {
		t.Errorf("Route path mismatch")
	}
}
