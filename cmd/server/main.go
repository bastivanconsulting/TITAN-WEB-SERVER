package main

import (
	"flag"
	"log"
	"os"
	"runtime"

	"titan-web-server/internal/config"
	"titan-web-server/internal/server"
)

func main() {
	configFile := flag.String("config", "titan-server.conf", "Path to configuration file")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

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

