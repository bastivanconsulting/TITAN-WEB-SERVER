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
