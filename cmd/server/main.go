package main

import (
	"flag"
	"log"
	"os"

	"custom-proxy/internal/config"
	"custom-proxy/internal/server"
)

func main() {
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
		os.Exit(1)
	}

	srv := server.NewServer(cfg, *configFile)
	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
