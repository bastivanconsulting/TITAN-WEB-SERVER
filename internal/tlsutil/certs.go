package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	RsaBits      = 2048
	ValidFor     = 365 * 24 * time.Hour
	ClusterCaDir = "/etc/titan/certs/cluster"
	CaCertFile = "ca.crt"
	CaKeyFile  = "ca.key"
)

// CertPaths holds the paths for a certificate and its key.
type CertPaths struct {
	CertPath string
	KeyPath  string
}

// GetClusterCaPaths returns the standard paths for the cluster's CA certificate and key.
func GetClusterCaPaths() CertPaths {
	return CertPaths{
		CertPath: filepath.Join(ClusterCaDir, CaCertFile),
		KeyPath:  filepath.Join(ClusterCaDir, CaKeyFile),
	}
}

// GetNodeCertPaths returns the standard paths for a specific node's certificate and key.
func GetNodeCertPaths(nodeID string) CertPaths {
	return CertPaths{
		CertPath: filepath.Join(ClusterCaDir, nodeID+".crt"),
		KeyPath:  filepath.Join(ClusterCaDir, nodeID+".key"),
	}
}

// fileExists checks if a file exists and is not a directory.
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// CertsExist checks if both certificate and key files exist at the given paths.
func (p *CertPaths) CertsExist() bool {
	return fileExists(p.CertPath) && fileExists(p.KeyPath)
}

// GenerateCA creates a new self-signed CA certificate and private key.
func GenerateCA(paths CertPaths) error {
	priv, err := rsa.GenerateKey(rand.Reader, RsaBits)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Titan Web Server Cluster"},
			CommonName:   "Cluster Internal CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(ValidFor * 10), // CA valid for 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(paths.CertPath), 0755); err != nil {
		return err
	}

	// Write cert
	certOut, err := os.Create(paths.CertPath)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return err
	}

	// Write key
	keyOut, err := os.OpenFile(paths.KeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	return pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
}

// GenerateNodeCert creates a new certificate for a node, signed by the CA.
func GenerateNodeCert(paths CertPaths, nodeID string, ips []net.IP, caCert *x509.Certificate, caKey interface{}) error {
	priv, err := rsa.GenerateKey(rand.Reader, RsaBits)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Titan Web Server Node"},
			CommonName:   "node." + nodeID,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(ValidFor),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: ips,
		DNSNames:    []string{"localhost", nodeID, "node." + nodeID},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return err
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(paths.CertPath), 0755); err != nil {
		return err
	}

	// Write cert
	certOut, err := os.Create(paths.CertPath)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return err
	}

	// Write key
	keyOut, err := os.OpenFile(paths.KeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	return pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
}

// LoadX509KeyPair loads a certificate and key from the given paths.
func LoadX509KeyPair(certFile, keyFile string) (*x509.Certificate, interface{}, error) {
	// Load CA cert
	caCertPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, nil, err
	}
	caCertBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	// Load CA key
	caKeyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return caCert, caKey, nil
}
