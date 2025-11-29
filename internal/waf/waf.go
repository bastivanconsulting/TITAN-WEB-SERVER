package waf

import (
	_ "embed"
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

//go:embed challenge.html
var challengeHTML []byte

// Logger interface for decoupling
type Logger interface {
	WAFLog(domain, remoteAddr, reason, details, country, userAgent string)
}

// WAF represents the main WAF module
type WAF struct {
	site        *config.Site
	geoIPDB     *geoip2.Reader
	rateLimiter *RateLimiter
	logger      Logger
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
func New(site *config.Site, geoIPDBPath string, logger Logger) (*WAF, error) {
	var db *geoip2.Reader
	var err error
	if geoIPDBPath != "" {
		db, err = geoip2.Open(geoIPDBPath)
		if err != nil {
			return nil, fmt.Errorf("could not open GeoIP database: %w", err)
		}
	}

	// Rate limiter is now initialized dynamically in the middleware
	return &WAF{
		site:    site,
		geoIPDB: db,
		logger:  logger,
	}, nil
}

// Middleware returns the HTTP middleware handler for the WAF
func (w *WAF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Determine which WAF config to use: location-specific or site-wide
		var activeWAFConfig *config.WAFConfig
		var matchedRoute *config.Route

		// Find the matching route
		for _, route := range w.site.Routes {
			// This is a simple prefix match, more complex matching might be needed
			if strings.HasPrefix(r.URL.Path, route.Path) {
				matchedRoute = &route
				break
			}
		}

		if matchedRoute != nil && matchedRoute.WAF != nil {
			activeWAFConfig = matchedRoute.WAF
		} else if w.site.WAF != nil {
			activeWAFConfig = w.site.WAF
		} else {
			// No WAF configured for this site or location
			next.ServeHTTP(rw, r)
			return
		}

		if !activeWAFConfig.Enabled {
			next.ServeHTTP(rw, r)
			return
		}

		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		// Whitelist check must come before anything else
		if isIPWhitelisted(ip, activeWAFConfig.AllowList) {
			next.ServeHTTP(rw, r)
			return
		}

		// Handle JS Challenge
		if activeWAFConfig.JSChallenge != nil && activeWAFConfig.JSChallenge.Enabled {
			if !w.handleJSChallenge(rw, r) {
				// Challenge is being presented or failed, so stop processing
				return
			}
		}


		// Under Attack Mode
		if activeWAFConfig.UnderAttackMode {
			w.logger.WAFLog(w.site.Domain, r.RemoteAddr, "UNDER_ATTACK_MODE", "All requests are temporarily blocked", "", r.UserAgent())
			http.Error(rw, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}

		// 1. IP Blacklist
		if isIPBlacklisted(ip, activeWAFConfig.DenyList) {
			w.logger.WAFLog(w.site.Domain, ip, "IP_BLACKLIST", "Client IP is in deny list", "", r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		// 2. Rate Limiting
		if activeWAFConfig.RateLimit > 0 {
			if w.rateLimiter == nil {
				// Initialize rate limiter on first use for this WAF instance
				limit := rate.Limit(float64(activeWAFConfig.RateLimit) / 60.0)
				w.rateLimiter = NewRateLimiter(limit, 1) // Burst can be configured if needed
			}
			if !w.rateLimiter.Allow(ip) {
				w.logger.WAFLog(w.site.Domain, ip, "RATE_LIMIT", fmt.Sprintf("Exceeded rate limit of %d/min", activeWAFConfig.RateLimit), "", r.UserAgent())
				http.Error(rw, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
		}

		// 3. GeoIP Blocking
		if country, blocked := w.isCountryBlocked(ip, activeWAFConfig.DenyCountries); blocked {
			w.logger.WAFLog(w.site.Domain, ip, "GEO_BLOCK", fmt.Sprintf("Country blocked: %s", country), country, r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		// 4. Basic Threat Detection (SQLi, XSS, LFI)
		if reason, details, malicious := w.isMalicious(r, activeWAFConfig); malicious {
			w.logger.WAFLog(w.site.Domain, ip, reason, details, "", r.UserAgent())
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(rw, r)
	})
}

func (w *WAF) handleJSChallenge(rw http.ResponseWriter, r *http.Request) bool {
	cookieName := "__titan_js_validation"
	expectedCookieValue := "passed" // In a real scenario, this would be dynamic (e.g., JWT, HMAC)

	// 1. Check for a valid cookie
	cookie, err := r.Cookie(cookieName)
	if err == nil && cookie.Value == expectedCookieValue {
		return true // Validation passed
	}

	// 2. Serve the challenge page
	rw.Header().Set("Content-Type", "text/html")
	rw.WriteHeader(http.StatusServiceUnavailable) // 503 is often used for this purpose
	rw.Write(challengeHTML)

	return false // Indicate that the response has been handled
}


// isMalicious checks for common attack patterns in the query and headers
func (w *WAF) isMalicious(r *http.Request, wafConfig *config.WAFConfig) (string, string, bool) {
	// Check query parameters
	for key, values := range r.URL.Query() {
		for _, value := range values {
			if wafConfig.CheckSQLi && detectSQLi(value) {
				return "SQLI", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
			if wafConfig.CheckXSS && detectXSS(value) {
				return "XSS", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
			if wafConfig.CheckLFI && detectLFI(value) {
				return "LFI", fmt.Sprintf("Found in query param '%s': %s", key, value), true
			}
		}
	}

	// Check headers (e.g., User-Agent, Referer)
	for name, values := range r.Header {
		for _, value := range values {
			if wafConfig.CheckSQLi && detectSQLi(value) {
				return "SQLI", fmt.Sprintf("Found in header '%s': %s", name, value), true
			}
			if wafConfig.CheckXSS && detectXSS(value) {
				return "XSS", fmt.Sprintf("Found in header '%s': %s", name, value), true
			}
		}
	}

	// Check User-Agent
	if wafConfig.CheckUA {
		ua := r.UserAgent()
		for _, pattern := range uaBlacklist {
			if strings.Contains(strings.ToLower(ua), pattern) {
				return "BAD_UA", fmt.Sprintf("Blacklisted User-Agent: %s", ua), true
			}
		}
	}

	// WordPress Protection
	if wafConfig.ProtectWP {
		for _, pattern := range wpPatterns {
			if strings.Contains(r.URL.Path, pattern) {
				return "WP_PROBE", fmt.Sprintf("Access to sensitive WordPress path: %s", r.URL.Path), true
			}
		}
	}

	// Joomla Protection
	if wafConfig.ProtectJoomla {
		for _, pattern := range joomlaPatterns {
			if strings.Contains(r.URL.Path, pattern) {
				return "JOOMLA_PROBE", fmt.Sprintf("Access to sensitive Joomla path: %s", r.URL.Path), true
			}
		}
	}

	// Generic PHP Protection
	if wafConfig.ProtectPHP {
		for key, values := range r.URL.Query() {
			for _, value := range values {
				for _, pattern := range phpPatterns {
					if strings.Contains(value, pattern) {
						return "PHP_INJECTION", fmt.Sprintf("Potential PHP injection in query param '%s': %s", key, value), true
					}
				}
			}
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

// WordPress protection rules
var wpPatterns = []string{
	"/wp-admin/", "/wp-login.php", "/xmlrpc.php",
}

// Joomla protection rules
var joomlaPatterns = []string{
	"/administrator/", "/index.php?option=com_users",
}

// Generic PHP protection rules
var phpPatterns = []string{
	"eval(", "base64_decode(", "gzinflate(", "shell_exec(", "passthru(",
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

func isIPWhitelisted(ip string, allowList []string) bool {
	for _, allowedIP := range allowList {
		if allowedIP == ip {
			return true
		}
		// Check CIDR
		if strings.Contains(allowedIP, "/") {
			_, ipNet, err := net.ParseCIDR(allowedIP)
			if err == nil {
				parsedIP := net.ParseIP(ip)
				if parsedIP != nil && ipNet.Contains(parsedIP) {
					return true
				}
			}
		}
	}
	return false
}

func isIPBlacklisted(ip string, denyList []string) bool {
	for _, deniedIP := range denyList {
		if deniedIP == ip {
			return true
		}
		// Check CIDR
		if strings.Contains(deniedIP, "/") {
			_, ipNet, err := net.ParseCIDR(deniedIP)
			if err == nil {
				parsedIP := net.ParseIP(ip)
				if parsedIP != nil && ipNet.Contains(parsedIP) {
					return true
				}
			}
		}
	}
	return false
}

func (w *WAF) isCountryBlocked(ipStr string, denyCountries []string) (string, bool) {
	if w.geoIPDB == nil || len(denyCountries) == 0 {
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

	for _, country := range denyCountries {
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
