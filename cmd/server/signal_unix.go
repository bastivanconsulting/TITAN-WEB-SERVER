//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"titan-web-server/internal/server"
)

func setupSignalHandler(srv *server.Server) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)

	go func() {
		for {
			sig := <-sigs
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP. Reloading certificates...")
				if err := srv.ReloadCerts(); err != nil {
					log.Printf("Error reloading certificates: %v", err)
				}
			}
		}
	}()
}
