package config

import (
	"testing"
	"titan-web-server/internal/config/parser"
	"github.com/stretchr/testify/assert"
)

func TestDecodeSSLPath(t *testing.T) {
	// Test case with ssl_path defined
	confWithSSLPath := `
		ssl_path "/var/lib/titan/ssl/";
	`
	p1 := parser.NewParser(confWithSSLPath)
	ast1, err := p1.Parse()
	assert.NoError(t, err)

	cfg1, err := DecodeAST(ast1)
	assert.NoError(t, err)
	assert.Equal(t, "/var/lib/titan/ssl/", cfg1.SSLPath)

	// Test case without ssl_path (should use default)
	confWithoutSSLPath := `
		http_port "8080";
	`
	p2 := parser.NewParser(confWithoutSSLPath)
	ast2, err := p2.Parse()
	assert.NoError(t, err)

	cfg2, err := DecodeAST(ast2)
	assert.NoError(t, err)
	assert.Equal(t, "/etc/titan/", cfg2.SSLPath)
}

func TestDecodeACMEBlock(t *testing.T) {
	conf := `
		acme {
			enabled on;
			email "test@example.com";
			directory_url "https://acme-staging-v02.api.letsencrypt.org/directory";
			cert_path "/tmp/certs";
		}
	`
	p := parser.NewParser(conf)
	ast, err := p.Parse()
	assert.NoError(t, err)

	cfg, err := DecodeAST(ast)
	assert.NoError(t, err)
	assert.NotNil(t, cfg.ACME)
	assert.True(t, cfg.ACME.Enabled)
	assert.Equal(t, "test@example.com", cfg.ACME.Email)
	assert.Equal(t, "https://acme-staging-v02.api.letsencrypt.org/directory", cfg.ACME.DirectoryURL)
	assert.Equal(t, "/tmp/certs", cfg.ACME.CertPath)
}
