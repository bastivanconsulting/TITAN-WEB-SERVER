package parser

import (
	"fmt"
	"unicode"
)

type Parser struct {
	input string
	pos   int
	len   int
}

func NewParser(input string) *Parser {
	return &Parser{
		input: input,
		pos:   0,
		len:   len(input),
	}
}

type TokenType int

const (
	TokenUnknown TokenType = iota
	TokenWord
	TokenSemicolon
	TokenLBrace
	TokenRBrace
	TokenEOF
)

type Token struct {
	Type  TokenType
	Value string
}

func (p *Parser) nextToken() Token {
	// Skip whitespace
	for p.pos < p.len && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}

	if p.pos >= p.len {
		return Token{Type: TokenEOF}
	}

	ch := p.input[p.pos]

	if ch == ';' {
		p.pos++
		return Token{Type: TokenSemicolon, Value: ";"}
	}
	if ch == '{' {
		p.pos++
		return Token{Type: TokenLBrace, Value: "{"}
	}
	if ch == '}' {
		p.pos++
		return Token{Type: TokenRBrace, Value: "}"}
	}
	if ch == '#' {
		// Comment, skip until newline
		p.pos++
		for p.pos < p.len && p.input[p.pos] != '\n' {
			p.pos++
		}
		return p.nextToken()
	}

	// Quoted string
	if ch == '"' || ch == '\'' {
		quote := ch
		p.pos++
		start := p.pos
		for p.pos < p.len && p.input[p.pos] != quote {
			if p.input[p.pos] == '\\' && p.pos+1 < p.len && p.input[p.pos+1] == quote {
				p.pos++ // Skip escape
			}
			p.pos++
		}
		value := p.input[start:p.pos]
		if p.pos < p.len {
			p.pos++ // Skip closing quote
		}
		// Unescape quotes if needed (simple version)
		return Token{Type: TokenWord, Value: value}
	}

	// Word
	start := p.pos
	for p.pos < p.len {
		c := p.input[p.pos]
		if unicode.IsSpace(rune(c)) || c == ';' || c == '{' || c == '}' || c == '#' {
			break
		}
		p.pos++
	}
	return Token{Type: TokenWord, Value: p.input[start:p.pos]}
}

func (p *Parser) Parse() (*ConfigAST, error) {
	root := &ConfigAST{}
	for {
		// We expect a directive or block name
		// But first check for EOF
		// Peek by saving pos? No, just call nextToken.

		// If we are at top level, we loop until EOF.
		// However, nextToken modifies state.

		// Let's implement a loop that consumes directives/blocks.

		// To peek, we rely on the fact that if we get EOF, we are done.
		// If we get '}', it's an error at top level (unexpected).

		tok := p.nextToken()
		if tok.Type == TokenEOF {
			break
		}
		if tok.Type != TokenWord {
			return nil, fmt.Errorf("expected directive name, got %v", tok.Value)
		}
		name := tok.Value

		params, block, isBlock, err := p.parseDirectiveOrBlock()
		if err != nil {
			return nil, err
		}

		if isBlock {
			root.Blocks = append(root.Blocks, Block{
				Name:       name,
				Parameters: params,
				Directives: block.Directives,
				Blocks:     block.Blocks,
			})
		} else {
			root.Directives = append(root.Directives, Directive{
				Name:       name,
				Parameters: params,
			})
		}
	}
	return root, nil
}

func (p *Parser) parseDirectiveOrBlock() ([]string, *ConfigAST, bool, error) {
	var params []string

	for {
		tok := p.nextToken()
		if tok.Type == TokenEOF {
			return nil, nil, false, fmt.Errorf("unexpected EOF")
		}

		if tok.Type == TokenSemicolon {
			return params, nil, false, nil
		}

		if tok.Type == TokenLBrace {
			content, err := p.parseBlockContent()
			return params, content, true, err
		}

		if tok.Type == TokenWord {
			params = append(params, tok.Value)
		} else {
			return nil, nil, false, fmt.Errorf("unexpected token %v", tok.Value)
		}
	}
}

func (p *Parser) parseBlockContent() (*ConfigAST, error) {
	block := &ConfigAST{}

	for {
		tok := p.nextToken()
		if tok.Type == TokenEOF {
			return nil, fmt.Errorf("unexpected EOF in block")
		}

		if tok.Type == TokenRBrace {
			return block, nil
		}

		if tok.Type != TokenWord {
			return nil, fmt.Errorf("expected directive name in block, got %v", tok.Value)
		}
		name := tok.Value

		params, subBlock, isBlock, err := p.parseDirectiveOrBlock()
		if err != nil {
			return nil, err
		}

		if isBlock {
			block.Blocks = append(block.Blocks, Block{
				Name:       name,
				Parameters: params,
				Directives: subBlock.Directives,
				Blocks:     subBlock.Blocks,
			})
		} else {
			block.Directives = append(block.Directives, Directive{
				Name:       name,
				Parameters: params,
			})
		}
	}
}
