package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"titan-web-server/internal/config"
	"titan-web-server/internal/server"
)

func main() {
	configFile := flag.String("config", "titan-server.conf", "Path to configuration file")
	debug := flag.Bool("debug", false, "Enable debug logging")
	version := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *version {
		v, err := os.ReadFile("VERSION")
		if err != nil {
			log.Fatalf("Failed to read version file: %v", err)
		}
		fmt.Println(string(v))
		os.Exit(0)
	}

	if _, err := os.Stat(*configFile); os.IsNotExist(err) {
		log.Printf("Configuration file not found. Creating a default one at %s", *configFile)
		err := createDefaultConfig(*configFile)
		if err != nil {
			log.Fatalf("Failed to create default config file: %v", err)
		}
	}

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
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


	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func createDefaultConfig(path string) error {
	defaultConfig := `
# Titan Web Server - Default Configuration

# HTTP port to listen on
http_port :8080;

# HTTPS port to listen on
# https_port :8443;

# Log directory
log_dir "/var/log/titan/";

# Example http block
http {
    server {
        server_name localhost;

        location / {
            root "/var/www/html";
        }
    }
}
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}
