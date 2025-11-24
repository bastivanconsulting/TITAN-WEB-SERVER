package proxy_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"custom-proxy/internal/cache"
	"custom-proxy/internal/proxy"
)

func TestProxyCache(t *testing.T) {
	// 1. Setup Mock Backend
	backendCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.Header().Set("X-Backend-Response", "true")
		fmt.Fprintf(w, "Hello from backend %d", backendCalls)
	}))
	defer backend.Close()

	// 2. Setup Cache
	c := cache.NewMemoryCache()

	// 3. Setup Proxy Handler
	opts := proxy.ProxyOptions{
		Target:   backend.URL,
		Cache:    c,
		CacheTTL: 1 * time.Second,
	}
	proxyHandler, err := proxy.NewProxyHandler(opts)
	if err != nil {
		t.Fatalf("Failed to create proxy handler: %v", err)
	}

	server := httptest.NewServer(proxyHandler)
	defer server.Close()

	// 4. First Request (Cache Miss)
	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "Hello from backend 1" {
		t.Errorf("Expected 'Hello from backend 1', got '%s'", body)
	}
	if backendCalls != 1 {
		t.Errorf("Expected 1 backend call, got %d", backendCalls)
	}
	if resp.Header.Get("X-Cache") == "HIT" {
		t.Errorf("Expected Cache MISS on first request")
	}

	// 5. Second Request (Cache Hit)
	resp, err = http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "Hello from backend 1" {
		t.Errorf("Expected 'Hello from backend 1' (cached), got '%s'", body)
	}
	// Backend calls should still be 1
	if backendCalls != 1 {
		t.Errorf("Expected 1 backend call, got %d", backendCalls)
	}
	if resp.Header.Get("X-Cache") != "HIT" {
		t.Errorf("Expected Cache HIT on second request")
	}

	// 6. Wait for Expiry
	time.Sleep(1100 * time.Millisecond)

	// 7. Third Request (Expired -> Miss)
	resp, err = http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "Hello from backend 2" {
		t.Errorf("Expected 'Hello from backend 2', got '%s'", body)
	}
	if backendCalls != 2 {
		t.Errorf("Expected 2 backend calls, got %d", backendCalls)
	}
}

func TestProxyCacheStatusCode(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated) // 201
		fmt.Fprint(w, "Created")
	}))
	defer backend.Close()

	c := cache.NewMemoryCache()
	opts := proxy.ProxyOptions{
		Target:   backend.URL,
		Cache:    c,
		CacheTTL: 1 * time.Second,
	}
	proxyHandler, err := proxy.NewProxyHandler(opts)
	if err != nil {
		t.Fatalf("Failed to create proxy handler: %v", err)
	}

	server := httptest.NewServer(proxyHandler)
	defer server.Close()

	// 1. First Request (Miss)
	resp, err := http.Get(server.URL + "/create")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected StatusCreated (201), got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Second Request (Hit)
	resp, err = http.Get(server.URL + "/create")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected StatusCreated (201) from cache, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cache") != "HIT" {
		t.Errorf("Expected Cache HIT")
	}
	resp.Body.Close()
}

func TestProxyCacheHeaders(t *testing.T) {
	// Scenarios: header key, header value, shouldCache
	scenarios := []struct {
		name        string
		headerKey   string
		headerValue string
		shouldCache bool
	}{
		{"NoCache", "Cache-Control", "no-cache", false},
		{"NoStore", "Cache-Control", "no-store", false},
		{"Private", "Cache-Control", "private", false},
		{"SetCookie", "Set-Cookie", "session=123", false},
		{"Vary", "Vary", "Accept-Encoding", false},
		{"Public", "Cache-Control", "public", true},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(sc.headerKey, sc.headerValue)
				fmt.Fprint(w, "Content")
			}))
			defer backend.Close()

			c := cache.NewMemoryCache()
			opts := proxy.ProxyOptions{
				Target:   backend.URL,
				Cache:    c,
				CacheTTL: 1 * time.Second,
			}
			proxyHandler, err := proxy.NewProxyHandler(opts)
			if err != nil {
				t.Fatalf("Failed to create proxy handler: %v", err)
			}
			server := httptest.NewServer(proxyHandler)
			defer server.Close()

			// 1. First Request
			resp, err := http.Get(server.URL + "/" + sc.name)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			resp.Body.Close()

			// 2. Second Request
			resp, err = http.Get(server.URL + "/" + sc.name)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			resp.Body.Close()

			isHit := resp.Header.Get("X-Cache") == "HIT"
			if isHit != sc.shouldCache {
				t.Errorf("Scenario %s: Expected cache hit %v, got %v", sc.name, sc.shouldCache, isHit)
			}
		})
	}
}

func TestProxyCacheLargeBody(t *testing.T) {
	// Mock backend returning 11MB
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11534336")
		// Write 11MB
		chunk := make([]byte, 1024*1024) // 1MB
		for i := 0; i < 11; i++ {
			w.Write(chunk)
		}
	}))
	defer backend.Close()

	c := cache.NewMemoryCache()
	opts := proxy.ProxyOptions{
		Target:   backend.URL,
		Cache:    c,
		CacheTTL: 1 * time.Second,
	}
	proxyHandler, err := proxy.NewProxyHandler(opts)
	if err != nil {
		t.Fatalf("Failed to create proxy handler: %v", err)
	}

	server := httptest.NewServer(proxyHandler)
	defer server.Close()

	// 1. Request (Should bypass cache because > 10MB)
	resp, err := http.Get(server.URL + "/large")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	// Consume body
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// 2. Check if cached
	// We can't easily check internal state of proxy, but we can check if it was added to cache
	// by generating the key manually or exposing a helper.
	// Or we just check if a second request is a HIT.

	resp, err = http.Get(server.URL + "/large")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if resp.Header.Get("X-Cache") == "HIT" {
		t.Errorf("Expected Cache MISS for large body")
	}
	resp.Body.Close()
}
