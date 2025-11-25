package parser

import (
	"testing"
)

func TestParser(t *testing.T) {
	input := `
user titan;
worker_processes 4;

http {
    server {
        listen 80;
        server_name example.com;

        location / {
            root /var/www/html;
        }
    }
}
`
	p := NewParser(input)
	ast, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(ast.Directives) != 2 {
		t.Errorf("Expected 2 top level directives, got %d", len(ast.Directives))
	}
	if ast.Directives[0].Name != "user" || ast.Directives[0].Parameters[0] != "titan" {
		t.Errorf("Unexpected first directive")
	}

	if len(ast.Blocks) != 1 {
		t.Fatalf("Expected 1 top level block, got %d", len(ast.Blocks))
	}

	httpBlock := ast.Blocks[0]
	if httpBlock.Name != "http" {
		t.Errorf("Expected http block, got %s", httpBlock.Name)
	}

	if len(httpBlock.Blocks) != 1 {
		t.Fatalf("Expected 1 server block inside http, got %d", len(httpBlock.Blocks))
	}

	serverBlock := httpBlock.Blocks[0]
	if serverBlock.Name != "server" {
		t.Errorf("Expected server block, got %s", serverBlock.Name)
	}

	foundListen := false
	for _, d := range serverBlock.Directives {
		if d.Name == "listen" {
			foundListen = true
			if d.Parameters[0] != "80" {
				t.Errorf("Expected listen 80, got %s", d.Parameters[0])
			}
		}
	}
	if !foundListen {
		t.Error("Did not find listen directive")
	}
}

func TestQuotedStrings(t *testing.T) {
	input := `
        key "value with spaces";
    `
	p := NewParser(input)
	ast, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(ast.Directives) != 1 {
		t.Fatalf("Expected 1 directive")
	}
	if ast.Directives[0].Parameters[0] != "value with spaces" {
		t.Errorf("Expected 'value with spaces', got '%s'", ast.Directives[0].Parameters[0])
	}
}
