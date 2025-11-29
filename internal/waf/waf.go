package waf

import (
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
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

// WAF represents the main WAF module using Coraza (ModSecurity for Go)
type WAF struct {
	site         *config.Site
	geoIPDB      *geoip2.Reader
	rateLimiter  *RateLimiter
	logger       Logger
	wafInstances map[*config.WAFConfig]coraza.WAF
	mu           sync.RWMutex
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

// New creates a new WAF instance with Coraza support
func New(site *config.Site, geoIPDBPath string, logger Logger) (*WAF, error) {
	var db *geoip2.Reader
	var err error
	if geoIPDBPath != "" {
		db, err = geoip2.Open(geoIPDBPath)
		if err != nil {
			return nil, fmt.Errorf("could not open GeoIP database: %w", err)
		}
	}

	w := &WAF{
		site:         site,
		geoIPDB:      db,
		logger:       logger,
		wafInstances: make(map[*config.WAFConfig]coraza.WAF),
	}

	// Initialize Coraza instances for the site-wide WAF config
	if site.WAF != nil {
		if err := w.initCoraza(site.WAF); err != nil {
			return nil, fmt.Errorf("failed to initialize site WAF: %w", err)
		}
	}

	// Initialize Coraza instances for route-specific WAF configs
	for _, route := range site.Routes {
		if route.WAF != nil {
			if err := w.initCoraza(route.WAF); err != nil {
				return nil, fmt.Errorf("failed to initialize route WAF for path %s: %w", route.Path, err)
			}
		}
	}

	return w, nil
}

// initCoraza initializes a Coraza WAF instance for a given configuration
func (w *WAF) initCoraza(cfg *config.WAFConfig) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Avoid re-initialization
	if _, exists := w.wafInstances[cfg]; exists {
		return nil
	}

	seclang := generateSeclang(cfg)

	// Create Coraza WAF instance
	wafInstance, err := coraza.NewWAF(coraza.NewWAFConfig().
		WithDirectives(seclang).
		WithErrorCallback(func(rule types.MatchedRule) {
			// This callback logs internal WAF errors, but we handle request blocking in the middleware
		}))

	if err != nil {
		return err
	}

	w.wafInstances[cfg] = wafInstance
	return nil
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

		// 4. ModSecurity/Coraza Processing
		if err := w.processCoraza(rw, r, activeWAFConfig); err != nil {
			// Request was interrupted by WAF
			// Log handled inside processCoraza or here
			w.logger.WAFLog(w.site.Domain, ip, "WAF_BLOCK", err.Error(), "", r.UserAgent())
			// Coraza already wrote the error to response if we used its Interruption helpers,
			// but we are using manual handling to fit Titan's logging style.
			if !isResponseWritten(err) {
				http.Error(rw, "Forbidden", http.StatusForbidden)
			}
			return
		}

		next.ServeHTTP(rw, r)
	})
}

// Helper to check if the error indicates the response was already written (not perfectly reliable but assumes 403)
func isResponseWritten(err error) bool {
	return false
}


func (w *WAF) processCoraza(rw http.ResponseWriter, r *http.Request, cfg *config.WAFConfig) error {
	w.mu.RLock()
	wafInstance, ok := w.wafInstances[cfg]
	w.mu.RUnlock()

	if !ok {
		// Should not happen if initialized correctly, fallback to allow
		return nil
	}

	tx := wafInstance.NewTransaction()
	defer tx.Close()

	// Information about the connection
	// For simplicity, we use the remote addr directly, assuming standard Go net/http behavior
	ip, port, _ := net.SplitHostPort(r.RemoteAddr)
	p, _ := net.LookupPort("tcp", port) // converting string port to int if needed, but ProcessConnection expects strings mostly or we just skip

	tx.ProcessConnection(ip, p, "", 0) // Destination info is optional/can be filled if known
	tx.ProcessURI(r.URL.String(), r.Method, r.Proto)

	// Process Headers
	for k, vv := range r.Header {
		for _, v := range vv {
			tx.AddRequestHeader(k, v)
		}
	}
	// Add Host header explicitly if missing in map
	if r.Host != "" {
		tx.AddRequestHeader("Host", r.Host)
	}

	if it := tx.ProcessRequestHeaders(); it != nil {
		return fmt.Errorf("blocked at request headers: Rule %d", it.RuleID)
	}

	// Process Body (Simplified: only if Content-Length > 0 and not too large)
	// In a real proxy, buffering body is expensive. We assume basic checking for now.
	// Coraza can handle streaming but here we keep it simple as Titan was doing basic checks.
	// If users want body inspection, they should ensure the body is available.
	if r.ContentLength > 0 && r.ContentLength < 1024*1024 { // Limit body inspection to 1MB
		// Check Content-Type to see if we should parse it
		// This is a simplified integration. For full body inspection, we'd need to wrap the body reader.
		// For now, we skip body inspection to avoid breaking streaming/proxying unless strictly needed.
		// If users want body inspection, they should ensure the body is available.
	}

	if it, _ := tx.ProcessRequestBody(); it != nil {
		return fmt.Errorf("blocked at request body: Rule %d", it.RuleID)
	}

	return nil
}

func (w *WAF) handleJSChallenge(rw http.ResponseWriter, r *http.Request) bool {
	cookieName := "__titan_js_validation"
	expectedCookieValue := "passed"

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

// generateSeclang builds a Seclang configuration based on the boolean flags.
// This allows migration from Titan's boolean flags to ModSecurity rules.
func generateSeclang(cfg *config.WAFConfig) string {
	var rules strings.Builder
	rules.WriteString("SecRuleEngine On\n")
	rules.WriteString("SecRequestBodyAccess On\n") // Needed for PHP/Body checks
	rules.WriteString("SecResponseBodyAccess Off\n")

	// 1. SQLi Protection
	if cfg.CheckSQLi {
		// Embed basic SQLi patterns converted to Seclang
		// Original regexes:
		// ' OR '1'='1
		// (?i)\bUNION\b.*\bSELECT\b
		// ...
		// We use a consolidated regex for simplicity in this migration
		rules.WriteString(`
SecRule ARGS|REQUEST_HEADERS "@rx (?i)(' OR '1'='1|union.*select|select.*from|update.*set|delete.*from|drop\s+table|information_schema|--|;|/\*|\*/|@@|@|char\(|nchar\(|varchar\(|nvarchar\(|alter\b|begin\b|cast\b|create\b|cursor\b|declare\b|exec\b|fetch\b|kill\b|open\b|sysobjects|syscolumns)" \
    "id:1001,phase:2,deny,status:403,msg:'SQL Injection Detected (Titan Default Rules)'"
`)
	}

	// 2. XSS Protection
	if cfg.CheckXSS {
		rules.WriteString(`
SecRule ARGS|REQUEST_HEADERS "@rx (?i)(<script>|javascript:|onload=|onerror=|onmouseover=|onclick=|ondblclick=|onmousedown=|onmouseup=|onmousemove=|onmouseout=|onkeypress=|onkeydown=|onkeyup=|alert\(|prompt\(|confirm\(|eval\(|src=|href='javascript:|data:|style=)" \
    "id:1002,phase:2,deny,status:403,msg:'XSS Detected (Titan Default Rules)'"
`)
	}

	// 3. LFI Protection
	if cfg.CheckLFI {
		rules.WriteString(`
SecRule ARGS|REQUEST_HEADERS "@rx (\.\./|/etc/passwd|php://|file://|zip://|phar://)" \
    "id:1003,phase:2,deny,status:403,msg:'LFI Detected (Titan Default Rules)'"
`)
	}

	// 4. User-Agent Blacklist
	if cfg.CheckUA {
		rules.WriteString(`
SecRule REQUEST_HEADERS:User-Agent "@rx (?i)(sqlmap|nikto|nmap|wpscan|masscan|zgrab|hydra|curl/7|wget/1)" \
    "id:1004,phase:1,deny,status:403,msg:'Blacklisted User-Agent'"
`)
	}

	// 5. WordPress Protection
	if cfg.ProtectWP {
		rules.WriteString(`
SecRule REQUEST_URI "@rx (/wp-admin/|/wp-login\.php|/xmlrpc\.php)" \
    "id:1005,phase:1,deny,status:403,msg:'WordPress Sensitive Path Access'"
`)
	}

	// 6. Joomla Protection
	if cfg.ProtectJoomla {
		rules.WriteString(`
SecRule REQUEST_URI "@rx (/administrator/|/index\.php\?option=com_users)" \
    "id:1006,phase:1,deny,status:403,msg:'Joomla Sensitive Path Access'"
`)
	}

	// 7. Generic PHP Protection
	if cfg.ProtectPHP {
		rules.WriteString(`
SecRule ARGS "@rx (?i)(eval\(|base64_decode\(|gzinflate\(|shell_exec\(|passthru\()" \
    "id:1007,phase:2,deny,status:403,msg:'PHP Injection Detected'"
`)
	}

	return rules.String()
}
