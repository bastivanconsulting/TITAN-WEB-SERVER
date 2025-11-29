//go:build windows

package main

import (
	"log"

	"titan-web-server/internal/server"
)

func setupSignalHandler(srv *server.Server) {
	log.Println("Signal handling for reload (SIGHUP) is not supported on Windows.")
}
