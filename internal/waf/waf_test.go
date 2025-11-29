package waf

import (
	"testing"
)

func TestDetectSQLi_FalsePositives(t *testing.T) {
	// The specific header reported by the user
	header := "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

	if detectSQLi(header) {
		t.Errorf("False positive detected for header: %s", header)
	}

    // Test for semicolon false positive which is common in headers
    cookie := "session_id=123; user=admin"
    if detectSQLi(cookie) {
        t.Errorf("False positive detected for cookie: %s", cookie)
    }

    // Test for common words that might be flagged by overly aggressive regex
    sentence := "I will create an open source project"
    if detectSQLi(sentence) {
        t.Errorf("False positive detected for sentence: %s", sentence)
    }

    email := "user@example.com"
    if detectSQLi(email) {
        t.Errorf("False positive detected for email: %s", email)
    }
}

func TestDetectSQLi_TruePositives(t *testing.T) {
	attacks := []string{
		"' OR '1'='1",
		"UNION SELECT * FROM users",
		"admin' --",
        "DROP TABLE users",
	}

	for _, attack := range attacks {
		if !detectSQLi(attack) {
			t.Errorf("Failed to detect SQLi: %s", attack)
		}
	}
}
