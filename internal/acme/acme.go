package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"titan-web-server/internal/config"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"crypto/x509"
	"encoding/pem"
	"errors"
)

// httpChallengeProvider implements the challenge.Provider interface.
type httpChallengeProvider struct {
	mu         sync.Mutex
	challenges map[string]string
}

func newHTTPChallengeProvider() *httpChallengeProvider {
	return &httpChallengeProvider{
		challenges: make(map[string]string),
	}
}

// Present makes the key authorization available for the ACME server.
func (p *httpChallengeProvider) Present(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.challenges[token] = keyAuth
	log.Printf("ACME: Presented challenge for domain %s, token %s", domain, token)
	return nil
}

// CleanUp removes the key authorization.
func (p *httpChallengeProvider) CleanUp(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.challenges, token)
	log.Printf("ACME: Cleaned up challenge for domain %s, token %s", domain, token)
	return nil
}

// User represents a Let's Encrypt user account.
type User struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *User) GetEmail() string {
	return u.Email
}
func (u *User) GetRegistration() *registration.Resource {
	return u.Registration
}
func (u *User) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

// ACMEManager handles the lifecycle of ACME certificates.
type ACMEManager struct {
	config          *config.Config
	client          *lego.Client
	user            *User
	challengeProvider *httpChallengeProvider
	ticker          *time.Ticker
	quit            chan struct{}
}

// NewManager creates a new ACMEManager.
func NewManager(cfg *config.Config) (*ACMEManager, error) {
	log.Println("Initializing ACME Manager")

	var acmeEmail string
	if cfg.ACME != nil && cfg.ACME.Email != "" {
		acmeEmail = cfg.ACME.Email
	} else {
		// Fallback to site-specific email for backward compatibility
		for _, site := range cfg.Sites {
			if site.SSL != nil && site.SSL.Provider == "acme" && site.SSL.Email != "" {
				acmeEmail = site.SSL.Email
				break
			}
		}
	}

	if acmeEmail == "" {
		acmeEmail = "contact@bastivan.consulting"
		log.Println("ACME: email not provided, using default contact@bastivan.consulting. Please update your configuration.")
	}

	keyPath := filepath.Join(cfg.SSLPath, "acme_account.key")
	var privateKey *ecdsa.PrivateKey
	keyBytes, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		log.Println("ACME: No account key found, generating a new one...")
		privateKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		keyBytes, err = x509.MarshalECPrivateKey(privateKey)
		if err != nil {
			return nil, err
		}
		pemKey := &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}
		err = os.WriteFile(keyPath, pem.EncodeToMemory(pemKey), 0600)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		log.Println("ACME: Found existing account key.")
		pemBlock, _ := pem.Decode(keyBytes)
		privateKey, err = x509.ParseECPrivateKey(pemBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	user := &User{
		Email: acmeEmail,
		key:   privateKey,
	}

	legoConfig := lego.NewConfig(user)
	if cfg.ACME != nil && cfg.ACME.DirectoryURL != "" {
		legoConfig.CADirURL = cfg.ACME.DirectoryURL
	} else {
		legoConfig.CADirURL = lego.LEDirectoryProduction
	}
	legoConfig.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return nil, err
	}

	challengeProvider := newHTTPChallengeProvider()
	err = client.Challenge.SetHTTP01Provider(challengeProvider)
	if err != nil {
		return nil, err
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, err
	}
	user.Registration = reg

	return &ACMEManager{
		config:          cfg,
		client:          client,
		user:            user,
		challengeProvider: challengeProvider,
	}, nil
}

// getPublicIP retrieves the public IPv4 address of the server.
func (m *ACMEManager) getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

// checkDomainDNS verifies that a domain points to the server's public IP.
func (m *ACMEManager) checkDomainDNS(domain string) (bool, error) {
	publicIP, err := m.getPublicIP()
	if err != nil {
		return false, err
	}

	ips, err := net.LookupHost(domain)
	if err != nil {
		return false, err
	}

	for _, ip := range ips {
		if ip == publicIP {
			return true, nil
		}
	}

	return false, nil
}

// CheckDomainsAndProvision issues certificates for all eligible domains.
func (m *ACMEManager) CheckDomainsAndProvision() {
	for _, site := range m.config.Sites {
		if site.SSL != nil && site.SSL.Provider == "acme" && site.SSL.Certificate == "" {
			log.Printf("Checking domain %s for ACME provisioning", site.Domain)
			valid, err := m.checkDomainDNS(site.Domain)
			if err != nil {
				log.Printf("DNS check for %s failed: %v", site.Domain, err)
				continue
			}

			if !valid {
				log.Printf("Domain %s does not point to this server's public IP", site.Domain)
				continue
			}

			// Placeholder for certificate issuance
			log.Printf("Domain %s is eligible for ACME certificate, attempting to provision", site.Domain)
			err = m.provisionCertificate(site.Domain)
			if err != nil {
				log.Printf("Failed to provision certificate for %s: %v", site.Domain, err)
			}
		}
	}
}

// HandleChallenge handles an incoming HTTP request to solve an ACME challenge.
// It returns true if the request was handled, otherwise false.
func (m *ACMEManager) HandleChallenge(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, http01.ChallengePath("")) {
		token := filepath.Base(r.URL.Path)
		m.challengeProvider.mu.Lock()
		keyAuth, ok := m.challengeProvider.challenges[token]
		m.challengeProvider.mu.Unlock()

		if ok {
			log.Printf("ACME: Responding to challenge for token %s", token)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(keyAuth))
			return true
		}
	}
	return false
}

func (m *ACMEManager) provisionCertificate(domain string) error {
	request := certificate.ObtainRequest{
		Domains: []string{domain},
		Bundle:  true,
	}
	certificates, err := m.client.Certificate.Obtain(request)
	if err != nil {
		return err
	}

	// Store the certificate
	certDir := filepath.Join(m.config.SSLPath, "certs", "acme")
	if m.config.ACME != nil && m.config.ACME.CertPath != "" {
		certDir = m.config.ACME.CertPath
	}
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(certDir, domain+".crt"), certificates.Certificate, 0600)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(certDir, domain+".key"), certificates.PrivateKey, 0600)
	if err != nil {
		return err
	}

	log.Printf("Certificate for %s stored successfully", domain)
	return nil
}

// checkCertificateExpiration checks if a certificate is close to expiring.
func (m *ACMEManager) checkCertificateExpiration(domain string) (bool, error) {
	certDir := filepath.Join(m.config.SSLPath, "certs", "acme")
	if m.config.ACME != nil && m.config.ACME.CertPath != "" {
		certDir = m.config.ACME.CertPath
	}
	certPath := filepath.Join(certDir, domain+".crt")
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Not expiring if it doesn't exist, but needs provisioning.
		}
		return false, err
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return false, errors.New("failed to parse certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}

	if time.Until(cert.NotAfter) < (20 * 24 * time.Hour) {
		log.Printf("Certificate for %s is nearing expiration (expires on %s)", domain, cert.NotAfter)
		return true, nil
	}

	return false, nil
}

// Start initiates the ACME manager's processes, such as renewal checks.
func (m *ACMEManager) Start() {
	log.Println("ACME Manager started")
	m.ticker = time.NewTicker(24 * time.Hour)
	m.quit = make(chan struct{})
	go func() {
		for {
			select {
			case <-m.ticker.C:
				log.Println("Checking for certificate renewals...")
				m.renewCertificates()
			case <-m.quit:
				m.ticker.Stop()
				return
			}
		}
	}()
}

// Stop terminates the ACME manager's background processes.
func (m *ACMEManager) Stop() {
	log.Println("ACME Manager stopped")
	close(m.quit)
}

func (m *ACMEManager) renewCertificates() {
	for _, site := range m.config.Sites {
		if site.SSL != nil && site.SSL.Provider == "acme" {
			needsRenewal, err := m.checkCertificateExpiration(site.Domain)
			if err != nil {
				log.Printf("Error checking expiration for %s: %v", site.Domain, err)
				continue
			}

			if needsRenewal {
				log.Printf("Renewing certificate for %s", site.Domain)
				err := m.provisionCertificate(site.Domain) // Re-provisioning to renew
				if err != nil {
					log.Printf("Failed to renew certificate for %s: %v", site.Domain, err)
				}
			}
		}
	}
}
