package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"

	"titan-web-server/internal/cluster"
	"titan-web-server/internal/config"
	"titan-web-server/internal/server"
	"titan-web-server/internal/tlsutil"
)

func main() {
	configFile := flag.String("config", "titan-server.conf", "Path to configuration file")
	nodeID := flag.String("node-id", "", "Unique Node ID for the cluster")
	raftAddr := flag.String("raft-addr", "127.0.0.1:9000", "Raft bind address")
	raftAdvertiseAddr := flag.String("raft-advertise-addr", "", "Raft advertise address. If not set, uses raft-addr.")
	raftDir := flag.String("raft-dir", "/etc/titan/raft", "Raft data directory")
	joinAddr := flag.String("join", "", "Address of a cluster node to join")
	bootstrap := flag.Bool("bootstrap", false, "Bootstrap the cluster (only for the first node)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Ensure raft dir exists
	if *nodeID != "" {
		os.MkdirAll(*raftDir, 0700)
		if err := ensureClusterCA(); err != nil {
			log.Fatalf("Failed to ensure cluster CA: %v", err)
		}
		if err := ensureNodeCert(*nodeID, *raftAdvertiseAddr); err != nil {
			log.Fatalf("Failed to ensure node certificate: %v", err)
		}
	}

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Printf("Warning: Could not load initial config: %v. Starting with empty config or waiting for cluster sync.", err)
		// If we are joining a cluster, we might not have a config yet.
		// If we are bootstrapping, we should probably have one.
		if !*bootstrap && *joinAddr == "" && *nodeID == "" {
			// Standalone mode, must have config
			log.Fatalf("Error loading config in standalone mode: %v", err)
		}
		if err != nil {
			cfg = &config.Config{} // Empty config to start
		}
	} else {
		// Validate config before starting
		if err := cfg.Validate(); err != nil {
			log.Fatalf("Invalid configuration: %v", err)
			os.Exit(1)
		}
	}

	// Set the number of threads for the runtime
	if cfg.MaxThreads > 0 {
		runtime.GOMAXPROCS(cfg.MaxThreads)
		log.Printf("GOMAXPROCS set to %d", cfg.MaxThreads)
	} else {
		log.Printf("GOMAXPROCS left at default value: %d", runtime.GOMAXPROCS(0))
	}

	srv := server.NewServer(cfg, *configFile, *debug)

	if *nodeID != "" {
		// Initialize Cluster Node
		log.Printf("Starting Raft node %s at %s", *nodeID, *raftAddr)

		fsm := cluster.NewFSM(cfg, *configFile, func(newCfg *config.Config) error {
			log.Println("Cluster config update received. Reloading server...")
			srv.ApplyConfig(newCfg)
			return nil
		})

		advertiseAddr := *raftAdvertiseAddr
		if advertiseAddr == "" {
			advertiseAddr = *raftAddr
		}

		node, err := cluster.NewNode(*nodeID, *raftAddr, advertiseAddr, *raftDir, fsm, *bootstrap)
		if err != nil {
			log.Fatalf("Failed to create raft node: %v", err)
		}

		srv.ClusterNode = node

		// Start certificate synchronization
		node.StartCertSync(cfg.AdminAPIKey, cfg.HTTPSPort)

		if *joinAddr != "" {
			// TODO: We need a way to tell the other node to add us.
			// Typically done via an HTTP API on the other node.
			// For now, let's assume the user will call the API on the leader manually or we implement a simple join request here if we had an HTTP client.
			log.Printf("Please add this node to the cluster via the leader's API: curl -X POST http://%s/admin/cluster/join -d '{\"id\": \"%s\", \"addr\": \"%s\"}'", *joinAddr, *nodeID, *raftAddr)
		}

		// If we are bootstrapping, we are the leader (eventually).
		// Config is already loaded in FSM via NewFSM.
	} else {
		// Standalone mode - use file watcher
		srv.WatchConfig()
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// ensureClusterCA checks for the existence of the cluster CA and generates it if it's missing.
func ensureClusterCA() error {
	caPaths := tlsutil.GetClusterCaPaths()
	if !caPaths.CertsExist() {
		log.Println("Cluster CA not found. Generating a new one...")
		return tlsutil.GenerateCA(caPaths)
	}
	log.Println("Existing cluster CA found.")
	return nil
}

// ensureNodeCert ensures a certificate for the current node exists, signed by the cluster CA.
func ensureNodeCert(nodeID, advertiseAddr string) error {
	nodePaths := tlsutil.GetNodeCertPaths(nodeID)
	if nodePaths.CertsExist() {
		// TODO: Check if cert is expiring soon and renew? For now, just check existence.
		log.Printf("Node certificate for %s found.", nodeID)
		return nil
	}

	log.Printf("Node certificate for %s not found. Generating a new one...", nodeID)

	// Load CA
	caPaths := tlsutil.GetClusterCaPaths()
	caCert, caKey, err := tlsutil.LoadX509KeyPair(caPaths.CertPath, caPaths.KeyPath)
	if err != nil {
		return fmt.Errorf("could not load cluster CA to sign node cert: %w", err)
	}

	// Get IPs for the certificate's SANs
	host, _, err := net.SplitHostPort(advertiseAddr)
	if err != nil {
		// Fallback for addresses without a port
		host = advertiseAddr
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		log.Printf("Warning: could not resolve advertise address '%s' to IP: %v. Using 127.0.0.1.", host, err)
		// Add a default IP if lookup fails, common in some container environments
		ips = []net.IP{net.ParseIP("127.0.0.1")}
	}

	return tlsutil.GenerateNodeCert(nodePaths, nodeID, ips, caCert, caKey)
}
