// Package main is the MeshedServerTool v3 entry point.
//
// MeshedServerTool is a web-based manager for SCP: 5k and SCP Pandemic
// dedicated servers. The v3 release is a full rewrite in Go, replacing
// the v2 Python/Flask implementation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/api"
	"github.com/Skomesh/MeshedServerTool/internal/config"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func main() {
	addr := flag.String("addr", "", "listen address (overrides config; e.g. :5000 or 127.0.0.1:5000)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate (enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "path to TLS private key (enables HTTPS)")
	dataDir := flag.String("data-dir", "", "override data directory (default: platform-specific user data dir)")
	flag.Parse()

	// Resolve data directory
	if *dataDir == "" {
		*dataDir = config.DefaultDataDir()
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	log.Printf("data dir: %s", *dataDir)

	// Open storage (Phase 1+ uses SQLite).
	store, err := storage.Open(filepath.Join(*dataDir, "meshed.db"))
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	// Build the server manager (Phase 2+). Rehydrates from storage.
	// Manager publishes state changes to the hub; the WebSocket endpoint
	// subscribes to the hub and pushes events to clients in real time.
	h := hub.NewHub()
	defer h.Close()
	manager, err := server.NewManager(store, h)
	if err != nil {
		log.Fatalf("init server manager: %v", err)
	}

	// Build router.
	router := api.NewRouter(store, manager, h, *dataDir)

	// Effective listen address
	listen := *addr
	if listen == "" {
		listen = ":5000"
	}

	srv := &http.Server{
		Addr:              listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run server in a goroutine so we can handle shutdown signals
	serverErr := make(chan error, 1)
	go func() {
		if *tlsCert != "" && *tlsKey != "" {
			log.Printf("meshed v3 listening on https://%s", listen)
			serverErr <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			log.Printf("meshed v3 listening on http://%s (TLS not enabled)", listen)
			serverErr <- srv.ListenAndServe()
		}
	}()

	// Wait for signal or fatal server error
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	case sig := <-stop:
		log.Printf("received %s, shutting down", sig)
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	fmt.Println("meshed v3 stopped")
}
