package htaccess

import (
	"bufio"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// RewriteCond represents a RewriteCond directive
type RewriteCond struct {
	TestString string
	Pattern    *regexp.Regexp
}

// RewriteRule represents a RewriteRule directive
type RewriteRule struct {
	Pattern     *regexp.Regexp
	Substitution string
	Conditions  []RewriteCond
}

// HTAccess represents a parsed .htaccess file
type HTAccess struct {
	RewriteRules []RewriteRule
}

// Parse parses a .htaccess file
func Parse(path string) (*HTAccess, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hta := &HTAccess{}
	var conditions []RewriteCond

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		directive := parts[0]
		args := parts[1:]

		switch strings.ToLower(directive) {
		case "rewritecond":
			if len(args) >= 2 {
				pattern, err := regexp.Compile(args[1])
				if err == nil {
					conditions = append(conditions, RewriteCond{
						TestString: args[0],
						Pattern:    pattern,
					})
				}
			}
		case "rewriterule":
			if len(args) >= 2 {
				pattern, err := regexp.Compile(args[0])
				if err == nil {
					rule := RewriteRule{
						Pattern:      pattern,
						Substitution: args[1],
						Conditions:   conditions,
					}
					hta.RewriteRules = append(hta.RewriteRules, rule)
				}
			}
			conditions = nil // Conditions only apply to the immediately following rule
		}
	}

	return hta, scanner.Err()
}

// Evaluate applies the rewrite rules to a given request URI
func (hta *HTAccess) Evaluate(uri string, requestHeader http.Header) string {
	for _, rule := range hta.RewriteRules {
		// Check conditions first
		conditionsMet := true
		for _, cond := range rule.Conditions {
			// Simple variable expansion
			testValue := ""
			switch cond.TestString {
			case "%{REQUEST_URI}":
				testValue = uri
			case "%{HTTP_HOST}":
				testValue = requestHeader.Get("Host")
			default:
				// Fallback for other variables or literal strings
				testValue = cond.TestString
			}

			if !cond.Pattern.MatchString(testValue) {
				conditionsMet = false
				break
			}
		}

		if conditionsMet && rule.Pattern.MatchString(uri) {
			return rule.Pattern.ReplaceAllString(uri, rule.Substitution)
		}
	}
	return uri
}
