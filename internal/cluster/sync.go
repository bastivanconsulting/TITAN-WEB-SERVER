package cluster

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/raft"
	"titan-web-server/internal/tlsutil"
)

const (
	SyncInterval      = 10 * time.Second
	CertsDir          = "/etc/titan/certs/"
	SyncEndpoint      = "/api/internal/certs/sync"
	FileEndpoint      = "/api/internal/certs/file"
)

type FileInfo struct {
	Path    string    `json:"path"`
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
}

// StartCertSync starts the certificate synchronization process if the node is the leader.
func (n *Node) StartCertSync(apiKey string, httpsPort string) {
	ticker := time.NewTicker(SyncInterval)
	go func() {
		for {
			<-ticker.C
			if n.Raft.State() == raft.Leader {
				if err := n.syncCertificates(apiKey, httpsPort); err != nil {
					log.Printf("Certificate sync error: %v", err)
				}
			}
		}
	}()
}

func (n *Node) syncCertificates(apiKey string, httpsPort string) error {
	manifest, err := n.buildManifest()
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	configFuture := n.Raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return fmt.Errorf("failed to get raft configuration: %w", err)
	}

	leaderID := n.Raft.Leader()
	for _, server := range configFuture.Configuration().Servers {
		if server.ID != raft.ServerID(leaderID) {
			go n.sendManifestToFollower(string(server.ID), string(server.Address), manifest, apiKey, httpsPort)
		}
	}

	return nil
}

func (n *Node) buildManifest() ([]FileInfo, error) {
	var manifest []FileInfo
	err := filepath.Walk(CertsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, err := filepath.Rel(CertsDir, path)
			if err != nil {
				return err
			}
			manifest = append(manifest, FileInfo{
				Path:    relPath,
				ModTime: info.ModTime(),
				Size:    info.Size(),
			})
		}
		return nil
	})
	return manifest, err
}

func (n *Node) sendManifestToFollower(followerID, addr string, manifest []FileInfo, apiKey string, httpsPort string) {
	jsonData, err := json.Marshal(manifest)
	if err != nil {
		log.Printf("Failed to marshal manifest for %s: %v", addr, err)
		return
	}

	// The follower's address from Raft includes the Raft port. We need to replace it
	// with the HTTPS port for the API call.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		log.Printf("Invalid follower address format '%s': %v", addr, err)
		return
	}
	// Reconstruct with the correct HTTPS port, trimming any leading colon from the port
	cleanPort := strings.TrimPrefix(httpsPort, ":")
	httpsAddr := net.JoinHostPort(host, cleanPort)

	url := fmt.Sprintf("https://%s%s", httpsAddr, SyncEndpoint)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to create request for %s: %v", addr, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	tlsConfig, err := createTLSConfig(n.ID, followerID)
	if err != nil {
		log.Printf("Failed to create TLS config for %s: %v", addr, err)
		return
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send manifest to %s: %v", addr, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Follower %s returned error on sync: %s", addr, resp.Status)
	}
}

// HandleSyncRequest is an HTTP handler for followers to receive a manifest.
func (n *Node) HandleSyncRequest(w http.ResponseWriter, r *http.Request, leaderAddress, apiKey, httpsPort string) {
	var remoteManifest []FileInfo
	if err := json.NewDecoder(r.Body).Decode(&remoteManifest); err != nil {
		http.Error(w, "Invalid manifest", http.StatusBadRequest)
		return
	}

	localManifest, err := n.buildManifest()
	if err != nil {
		http.Error(w, "Could not build local manifest", http.StatusInternalServerError)
		return
	}

	localFiles := make(map[string]FileInfo)
	for _, info := range localManifest {
		localFiles[info.Path] = info
	}

	leaderID := string(n.Raft.Leader())
	for _, remoteInfo := range remoteManifest {
		localInfo, exists := localFiles[remoteInfo.Path]
		if !exists || localInfo.ModTime.Before(remoteInfo.ModTime) || localInfo.Size != remoteInfo.Size {
			if err := n.downloadFile(leaderID, leaderAddress, remoteInfo.Path, apiKey, httpsPort); err != nil {
				log.Printf("Failed to download %s from leader: %v", remoteInfo.Path, err)
				// Continue trying to sync other files
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (n *Node) downloadFile(leaderID, leaderAddr, path, apiKey, httpsPort string) error {
	// The leader's address from Raft includes the Raft port. We need to replace it
	// with the HTTPS port for the API call.
	host, _, err := net.SplitHostPort(leaderAddr)
	if err != nil {
		log.Printf("Invalid leader address format '%s': %v", leaderAddr, err)
		// Fallback to using the address as-is, just in case it's already correct.
		host = leaderAddr
	}

	// Reconstruct with the correct HTTPS port
	cleanPort := strings.TrimPrefix(httpsPort, ":")
	httpsAddr := net.JoinHostPort(host, cleanPort)

	url := fmt.Sprintf("https://%s%s?file=%s", httpsAddr, FileEndpoint, path)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", apiKey)

	tlsConfig, err := createTLSConfig(n.ID, leaderID)
	if err != nil {
		return fmt.Errorf("could not create tls config for download: %w", err)
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("leader returned status %s", resp.Status)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	destPath := filepath.Join(CertsDir, path)
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	return os.WriteFile(destPath, content, 0644)
}

// HandleFileRequest is an HTTP handler for the leader to serve certificate files.
func (n *Node) HandleFileRequest(w http.ResponseWriter, r *http.Request) {
	if n.Raft.State() != raft.Leader {
		http.Error(w, "Not a leader", http.StatusServiceUnavailable)
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Error(w, "Missing file parameter", http.StatusBadRequest)
		return
	}

	// Sanitize the file path to prevent path traversal attacks.
	cleanPath := filepath.Clean(filepath.Join(CertsDir, filePath))
	if !filepath.HasPrefix(cleanPath, CertsDir) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	http.ServeFile(w, r, cleanPath)
}

func createTLSConfig(clientNodeID, targetNodeID string) (*tls.Config, error) {
	// Load node's keypair
	nodePaths := tlsutil.GetNodeCertPaths(clientNodeID)
	cert, err := tls.LoadX509KeyPair(nodePaths.CertPath, nodePaths.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("could not load node TLS key pair: %w", err)
	}

	// Load CA cert
	caPaths := tlsutil.GetClusterCaPaths()
	caCertPEM, err := os.ReadFile(caPaths.CertPath)
	if err != nil {
		return nil, fmt.Errorf("could not read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertPEM)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   "node." + targetNodeID,
	}, nil
}
