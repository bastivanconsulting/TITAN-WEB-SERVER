package main

import (
	"flag"
	"log"
	"os"

	"titan-web-server/internal/config"
	"titan-web-server/internal/server"
)

func main() {
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
		os.Exit(1)
	}

	// Validate config before starting
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	srv := server.NewServer(cfg, *configFile)

	// Start config watcher
	srv.WatchConfig()

	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
