package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger handles access and error logging
type Logger struct {
	mu      sync.RWMutex
	logDir  string
	files   map[string]*os.File
	enabled bool
}

// NewLogger creates a new logger instance
func NewLogger(logDir string) *Logger {
	// If logDir is empty, default to /var/log/titan/
	// However, if we can't write there, we might need a fallback or fail.
	// We will attempt to use it.
	if logDir == "" {
		logDir = "/var/log/titan/"
	}

	return &Logger{
		logDir:  logDir,
		files:   make(map[string]*os.File),
		enabled: true,
	}
}

// EnsureDir checks if the log directory exists, creates it if not.
// Returns error if it fails.
func (l *Logger) EnsureDir() error {
	if !l.enabled {
		return nil
	}
	info, err := os.Stat(l.logDir)
	if os.IsNotExist(err) {
		return os.MkdirAll(l.logDir, 0755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", l.logDir)
	}
	return nil
}

// getFile returns a file handle for the given domain and log type (access/error)
// It manages opening and storing the handle.
func (l *Logger) getFile(domain, logType string) (io.Writer, error) {
	// sanitize domain
	safeDomain := strings.ReplaceAll(domain, "/", "_")
	safeDomain = strings.ReplaceAll(safeDomain, "\\", "_")
	safeDomain = strings.ReplaceAll(safeDomain, ":", "_")

	filename := fmt.Sprintf("%s-%s.log", safeDomain, logType)
	path := filepath.Join(l.logDir, filename)

	l.mu.RLock()
	f, ok := l.files[path]
	l.mu.RUnlock()
	if ok {
		return f, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Double check
	if f, ok := l.files[path]; ok {
		return f, nil
	}

	// Open file
	// O_APPEND | O_CREATE | O_WRONLY
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	l.files[path] = f
	return f, nil
}

// AccessLog writes an entry to the access log
func (l *Logger) AccessLog(domain, method, path, remoteAddr string, status int, duration time.Duration) {
	if !l.enabled {
		return
	}

	// Format: [Time] ClientIP Method Path Status Duration
	line := fmt.Sprintf("[%s] %s %s %s %d %v\n",
		time.Now().Format(time.RFC3339),
		remoteAddr, method, path, status, duration,
	)

	w, err := l.getFile(domain, "access")
	if err != nil {
		fmt.Printf("Error writing access log for %s: %v\n", domain, err)
		return
	}
	w.Write([]byte(line))
}

// WAFLog writes a WAF event to a dedicated log
func (l *Logger) WAFLog(domain, remoteAddr, reason, details, country, userAgent string) {
	if !l.enabled {
		return
	}

	// Format: [Time] ClientIP Country UserAgent Reason Details
	line := fmt.Sprintf("[%s] %s %s \"%s\" %s %s\n",
		time.Now().Format(time.RFC3339),
		remoteAddr, country, userAgent, reason, details,
	)

	w, err := l.getFile(domain, "waf")
	if err != nil {
		fmt.Printf("Error writing WAF log for %s: %v\n", domain, err)
		return
	}
	w.Write([]byte(line))
}

// ErrorLog writes an entry to the error log
func (l *Logger) ErrorLog(domain, msg string) {
	if !l.enabled {
		return
	}

	line := fmt.Sprintf("[%s] ERROR: %s\n", time.Now().Format(time.RFC3339), msg)

	w, err := l.getFile(domain, "error")
	if err != nil {
		fmt.Printf("Error writing error log for %s: %v\n", domain, err)
		return
	}
	w.Write([]byte(line))
}

// Close closes all open log files
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range l.files {
		f.Close()
	}
	l.files = make(map[string]*os.File)
}
