package waf

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
	"golang.org/x/time/rate"

	"titan-web-server/internal/config"
)

// Logger interface for decoupling
type Logger interface {
	WAFLog(domain, remoteAddr, reason, details, country, userAgent string)
}

// WAF represents the main WAF module
type WAF struct {
	config      *config.WAF
	geoIPDB     *geoip2.Reader
	rateLimiter *RateLimiter
	logger      Logger
	domain      string
}

// RateLimiter stores rate limiters for each IP
type RateLimiter struct {
	sync.Mutex
	visitors map[string]*visitor
	limit    rate.Limit
	burst    int
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New creates a new WAF instance
func New(cfg *config.WAF, geoIPDBPath string, logger Logger, domain string) (*WAF, error) {
	var db *geoip2.Reader
	var err error
	if geoIPDBPath != "" {
		db, err = geoip2.Open(geoIPDBPath)
		if err != nil {
			return nil, fmt.Errorf("could not open GeoIP database: %w", err)
		}
	}

	// Convert rate limit from requests per minute to requests per second
	limit := rate.Limit(float64(cfg.RateLimit) / 60.0)

	return &WAF{
		config:      cfg,
		geoIPDB:     db,
		rateLimiter: NewRateLimiter(limit, 1),
		logger:      logger,
		domain:      domain,
	}, nil
}

// Middleware returns the HTTP middleware handler for the WAF
func (w *WAF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Under Attack Mode
		if w.config.UnderAttackMode {
			// For now, just block all requests.
			// Later, this could be a JS challenge or CAPTCHA.
			w.logger.WAFLog(w.domain, r.RemoteAddr, "UNDER_ATTACK_MODE", "All requests are temporarily blocked", "", r.UserAgent())
			http.Error(rw, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}

		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		// 1. IP Whitelist/Blacklist
		if w.isIPBlocked(ip) {
			w.logger.WAFLog(w.domain, ip, "IP_BLACKLIST", "Client IP is in deny list", "", r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		// 2. Rate Limiting
		if !w.rateLimiter.Allow(ip) {
			w.logger.WAFLog(w.domain, ip, "RATE_LIMIT", fmt.Sprintf("Exceeded rate limit of %d/min", w.config.RateLimit), "", r.UserAgent())
			http.Error(rw, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		// 3. GeoIP Blocking
		if country, blocked := w.isCountryBlocked(ip); blocked {
			w.logger.WAFLog(w.domain, ip, "GEO_BLOCK", fmt.Sprintf("Country blocked: %s", country), country, r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		// 4. Basic Threat Detection (SQLi, XSS, LFI)
		if reason, details, malicious := w.isMalicious(r); malicious {
			w.logger.WAFLog(w.domain, ip, reason, details, "", r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(rw, r)
	})
}

// isMalicious checks for common attack patterns in the query and headers
func (w *WAF) isMalicious(r *http.Request) (string, string, bool) {
	// Check query parameters
	for key, values := range r.URL.Query() {
		for _, value := range values {
			if detectSQLi(value) {
				return "SQLI", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
			if detectXSS(value) {
				return "XSS", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
			if detectLFI(value) {
				return "LFI", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
		}
	}

	// Check headers (e.g., User-Agent, Referer)
	for name, values := range r.Header {
		for _, value := range values {
			if detectSQLi(value) {
				return "SQLI", fmt.Sprintf("Found in header '%s': %s", name, value), true
			}
			if detectXSS(value) {
				return "XSS", fmt.Sprintf("Found in header '%s': %s", name, value), true
			}
		}
	}

	// Check User-Agent
	ua := r.UserAgent()
	for _, pattern := range uaBlacklist {
		if strings.Contains(strings.ToLower(ua), pattern) {
			return "BAD_UA", fmt.Sprintf("Blacklisted User-Agent: %s", ua), true
		}
	}

	return "", "", false
}

// Compiled regex for SQLi detection
var sqliRegexPatterns []*regexp.Regexp

func init() {
	// More specific SQLi detection patterns using regex with word boundaries
	patterns := []string{
		`' OR '1'='1`, `(?i)\bUNION\b.*\bSELECT\b`, `(?i)\bSELECT\b.*\bFROM\b`, `(?i)\bUPDATE\b.*\bSET\b`, `(?i)\bDELETE\b.*\bFROM\b`,
		`(?i)\bDROP\b\s+\bTABLE\b`, `(?i)information_schema`,
		`--`, `;`, `/\*`, `\*/`, `@@`, `@`, `(?i)\bchar\b\(`, `(?i)\bnchar\b\(`, `(?i)\bvarchar\b\(`, `(?i)\bnvarchar\b\(`,
		`(?i)\balter\b`, `(?i)\bbegin\b`, `(?i)\bcast\b`, `(?i)\bcreate\b`, `(?i)\bcursor\b`, `(?i)\bdeclare\b`, `(?i)\bend\b`,
		`(?i)\bexec\b`, `(?i)\bfetch\b`, `(?i)\bkill\b`, `(?i)\bopen\b`, `(?i)\bsysobjects\b`, `(?i)\bsyscolumns\b`,
	}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			// This should not happen with the tested patterns.
			// If it does, it's a programmer error, so panic is acceptable.
			panic(fmt.Sprintf("Failed to compile regex '%s': %v", p, err))
		}
		sqliRegexPatterns = append(sqliRegexPatterns, re)
	}
}

// Very basic XSS detection patterns
var xssPatterns = []string{
	"<script>", "javascript:", "onload=", "onerror=", "onmouseover=", "onclick=", "ondblclick=", "onmousedown=", "onmouseup=",
	"onmousemove=", "onmouseout=", "onkeypress=", "onkeydown=", "onkeyup=", "alert(", "prompt(", "confirm(", "eval(", "src=",
	"href='javascript:", "data:", "style=",
}

// Very basic LFI detection patterns
var lfiPatterns = []string{
	"../", "/etc/passwd", "php://", "file://", "zip://", "phar://",
}

// Basic User-Agent blacklist
var uaBlacklist = []string{
	"sqlmap", "nikto", "nmap", "wpscan", "masscan", "zgrab", "hydra", "curl/7", "wget/1",
}

func detectSQLi(input string) bool {
	for _, re := range sqliRegexPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

func detectXSS(input string) bool {
	for _, pattern := range xssPatterns {
		if strings.Contains(strings.ToLower(input), strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func detectLFI(input string) bool {
	for _, pattern := range lfiPatterns {
		if strings.Contains(input, pattern) {
			return true
		}
	}
	return false
}

func (w *WAF) isIPBlocked(ip string) bool {
	// Whitelist takes precedence
	for _, allowedIP := range w.config.AllowList {
		if allowedIP == ip {
			return false
		}
	}
	// Check blacklist
	for _, deniedIP := range w.config.DenyList {
		if deniedIP == ip {
			return true
		}
	}
	return false
}

func (w *WAF) isCountryBlocked(ipStr string) (string, bool) {
	if w.geoIPDB == nil || len(w.config.DenyCountries) == 0 {
		return "", false
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", false
	}

	record, err := w.geoIPDB.Country(ip)
	if err != nil {
		return "", false // Fail open
	}

	for _, country := range w.config.DenyCountries {
		if strings.EqualFold(record.Country.IsoCode, country) {
			return record.Country.IsoCode, true
		}
	}

	return "", false
}

// NewRateLimiter creates a new RateLimiter
func NewRateLimiter(r rate.Limit, b int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		limit:    r,
		burst:    b,
	}
	go rl.cleanupWorker()
	return rl
}

func (rl *RateLimiter) cleanupWorker() {
	for {
		time.Sleep(1 * time.Minute)
		rl.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.Unlock()
	}
}

// Allow checks if a request from a given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.Lock()
	defer rl.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.limit, rl.burst)
		v = &visitor{limiter: limiter}
		rl.visitors[ip] = v
	}

	v.lastSeen = time.Now()
	return v.limiter.Allow()
}

// Close closes the GeoIP database connection
func (w *WAF) Close() {
	if w.geoIPDB != nil {
		w.geoIPDB.Close()
	}
}
