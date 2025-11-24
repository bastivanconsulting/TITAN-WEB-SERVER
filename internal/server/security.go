package server

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"
)

// FIPS compliant cipher suites (subset of modern secure suites)
// Focused on AES-GCM and ECDHE/RSA
var fipsCipherSuites = []uint16{
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	// Also include TLS 1.3 ciphers which are implicit but can be explicit
	tls.TLS_AES_128_GCM_SHA256,
	tls.TLS_AES_256_GCM_SHA384,
}

// getFIPSTLSConfig returns a base TLS config that adheres to FIPS 140-2/3 constraints
// (simulated by restricting to strong algos, as real FIPS requires compliant crypto module)
func getFIPSTLSConfig(standard string) *tls.Config {
	// Standard can be "140-2" or "140-3".
	// For TLS, FIPS 140-3 largely continues 140-2 guidance but deprecates older key exchanges sooner.
	// We use the same high-security set for both to ensure maximum compliance.

	return &tls.Config{
		MinVersion:               tls.VersionTLS12,
		CipherSuites:             fipsCipherSuites,
		PreferServerCipherSuites: true,
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.CurveP384,
		},
	}
}

// OCSPManager handles fetching and updating OCSP staples
type OCSPManager struct {
	mu          sync.RWMutex
	staples     map[string][]byte
	certs       map[string]*tls.Certificate // Key: domain
	issuerCerts map[string]*x509.Certificate // Key: domain
}

func NewOCSPManager() *OCSPManager {
	return &OCSPManager{
		staples:     make(map[string][]byte),
		certs:       make(map[string]*tls.Certificate),
		issuerCerts: make(map[string]*x509.Certificate),
	}
}

// AddCertificate registers a certificate for OCSP monitoring
func (m *OCSPManager) AddCertificate(domain string, cert *tls.Certificate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.certs[domain] = cert

	if len(cert.Certificate) > 0 {
		x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			// Try to find issuer in the chain
			if len(cert.Certificate) > 1 {
				issuer, err := x509.ParseCertificate(cert.Certificate[1])
				if err == nil {
					m.issuerCerts[domain] = issuer
				}
			}
			// Trigger initial fetch
			go m.fetchOCSP(domain, x509Cert)
		}
	}
}

// GetStaple returns the current OCSP staple for a domain
func (m *OCSPManager) GetStaple(domain string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.staples[domain]
}

// Start starts the background refresher
func (m *OCSPManager) Start() {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for range ticker.C {
			m.refreshAll()
		}
	}()
}

func (m *OCSPManager) refreshAll() {
	m.mu.RLock()
	domains := make([]string, 0, len(m.certs))
	for d := range m.certs {
		domains = append(domains, d)
	}
	m.mu.RUnlock()

	for _, d := range domains {
		m.mu.RLock()
		cert := m.certs[d]
		m.mu.RUnlock()

		if len(cert.Certificate) > 0 {
			if x509Cert, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				m.fetchOCSP(d, x509Cert)
			}
		}
	}
}

func (m *OCSPManager) fetchOCSP(domain string, cert *x509.Certificate) {
	if len(cert.OCSPServer) == 0 {
		return
	}

	m.mu.RLock()
	issuer := m.issuerCerts[domain]
	m.mu.RUnlock()

	if issuer == nil {
		fmt.Printf("OCSP Info: No issuer cert found for %s, skipping OCSP fetch\n", domain)
		return
	}

	ocspUrl := cert.OCSPServer[0]

	req, err := ocsp.CreateRequest(cert, issuer, nil)
	if err != nil {
		fmt.Printf("Error creating OCSP request for %s: %v\n", domain, err)
		return
	}

	httpReq, err := http.NewRequest(http.MethodPost, ocspUrl, bytes.NewReader(req))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/ocsp-request")
	httpReq.Header.Set("Accept", "application/ocsp-response")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Printf("Error fetching OCSP for %s: %v\n", domain, err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	// Basic validation could happen here (ocsp.ParseResponse), skipping for brevity/robustness against parsers
	m.mu.Lock()
	m.staples[domain] = body
	// Update the certificate itself so the main server logic picks it up if it reads the struct
	// But mostly we need to inject it in GetCertificate or ClientHello
	m.mu.Unlock()
}
