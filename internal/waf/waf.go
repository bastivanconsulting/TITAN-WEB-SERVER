package waf

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// WAF represents the Web Application Firewall.
type WAF struct {
	rules []string
}

// New creates a new WAF instance with the given rule files.
func New(ruleFiles []string) (*WAF, error) {
	var rules []string
	for _, file := range ruleFiles {
		r, err := loadRules(file)
		if err != nil {
			return nil, fmt.Errorf("failed to load rules from %s: %w", file, err)
		}
		rules = append(rules, r...)
	}
	return &WAF{rules: rules}, nil
}

// loadRules loads rules from a file.
func loadRules(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rules []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rules = append(rules, scanner.Text())
	}
	return rules, scanner.Err()
}

// CheckRequest checks if the request is malicious.
func (w *WAF) CheckRequest(r *http.Request) bool {
	for _, rule := range w.rules {
		if strings.Contains(r.URL.String(), rule) {
			return true
		}
	}
	return false
}
