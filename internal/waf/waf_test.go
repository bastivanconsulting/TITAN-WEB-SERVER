package waf

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"titan-web-server/internal/config"
)

type MockLogger struct{}

func (m *MockLogger) WAFLog(domain, remoteAddr, reason, details, country, userAgent string) {
	// No-op
}

func TestWAF_ModSecurity_SQLi(t *testing.T) {
	// 1. Setup
	siteCfg := &config.Site{
		Domain: "example.com",
		WAF: &config.WAFConfig{
			Enabled:   true,
			CheckSQLi: true,
		},
	}

	w, err := New(siteCfg, "", &MockLogger{})
	if err != nil {
		t.Fatalf("Failed to create WAF: %v", err)
	}

	// 2. Middleware
	handler := w.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// 3. Test Cases
	tests := []struct {
		name       string
		query      string
		expectCode int
	}{
		{"Normal Request", "q=hello", http.StatusOK},
		{"SQLi UNION SELECT", "q=UNION+SELECT+1", http.StatusForbidden},
		{"SQLi OR 1=1", "q=%27+OR+%271%27%3D%271", http.StatusForbidden}, // URL Encoded
		{"SQLi Comment", "q=admin%27+--", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct valid URL
			u := "http://example.com/?" + tt.query
			// Verify it parses
			if _, err := url.Parse(u); err != nil {
				t.Fatalf("Invalid URL in test case: %v", err)
			}

			req := httptest.NewRequest("GET", u, nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectCode {
				t.Errorf("Expected status %d, got %d for query %s", tt.expectCode, rr.Code, tt.query)
			}
		})
	}
}

func TestWAF_ModSecurity_XSS(t *testing.T) {
	siteCfg := &config.Site{
		Domain: "example.com",
		WAF: &config.WAFConfig{
			Enabled:  true,
			CheckXSS: true,
		},
	}

	w, err := New(siteCfg, "", &MockLogger{})
	if err != nil {
		t.Fatalf("Failed to create WAF: %v", err)
	}

	handler := w.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for XSS, got %d", rr.Code)
	}
}

func TestWAF_IPWhitelisting_Precedes_ModSec(t *testing.T) {
	siteCfg := &config.Site{
		Domain: "example.com",
		WAF: &config.WAFConfig{
			Enabled:   true,
			CheckSQLi: true, // Should block if checked
			AllowList: []string{"192.168.1.5"},
		},
	}

	w, err := New(siteCfg, "", &MockLogger{})
	if err != nil {
		t.Fatalf("Failed to create WAF: %v", err)
	}

	handler := w.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request with SQLi BUT from whitelisted IP
	req := httptest.NewRequest("GET", "http://example.com/?q=UNION+SELECT", nil)
	req.RemoteAddr = "192.168.1.5:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for whitelisted IP even with SQLi payload, got %d", rr.Code)
	}
}

func TestWAF_RouteSpecificConfig(t *testing.T) {
	siteCfg := &config.Site{
		Domain: "example.com",
		Routes: []config.Route{
			{
				Path: "/api/",
				WAF: &config.WAFConfig{
					Enabled:   true,
					CheckSQLi: true,
				},
			},
			{
				Path: "/public/",
				WAF: &config.WAFConfig{
					Enabled:   false, // WAF disabled here
					CheckSQLi: true,
				},
			},
		},
	}

	w, err := New(siteCfg, "", &MockLogger{})
	if err != nil {
		t.Fatalf("Failed to create WAF: %v", err)
	}

	handler := w.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 1. Attack on protected route
	req1 := httptest.NewRequest("GET", "http://example.com/api/data?q=UNION+SELECT", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusForbidden {
		t.Errorf("Expected 403 on protected route, got %d", rr1.Code)
	}

	// 2. Attack on unprotected route
	req2 := httptest.NewRequest("GET", "http://example.com/public/data?q=UNION+SELECT", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("Expected 200 on unprotected route, got %d", rr2.Code)
	}
}
