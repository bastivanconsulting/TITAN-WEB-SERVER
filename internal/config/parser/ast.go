package parser

// Directive represents a simple key-value(s) pair like "listen 80;"
type Directive struct {
	Name       string
	Parameters []string
}

// Block represents a block like "server { ... }"
type Block struct {
	Name       string
	Parameters []string
	Directives []Directive
	Blocks     []Block
}

// ConfigAST represents the root of the parsed configuration
type ConfigAST struct {
	Directives []Directive
	Blocks     []Block
}
