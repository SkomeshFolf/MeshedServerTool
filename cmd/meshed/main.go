// Package main is the MeshedServerTool v3 entry point.
//
// MeshedServerTool is a web-based manager for SCP: 5k and SCP Pandemic
// dedicated servers. The v3 release is a full rewrite in Go, replacing
// the v2 Python/Flask implementation.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/Skomesh/MeshedServerTool/internal/api"
	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/config"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/motd"
	"github.com/Skomesh/MeshedServerTool/internal/reports"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func main() {
	addr := flag.String("addr", "", "listen address (overrides config; e.g. :5000 or 127.0.0.1:5000)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate (enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "path to TLS private key (enables HTTPS)")
	autocertDomain := flag.String("autocert-domain", "", "domain for Let's Encrypt autocert (e.g. mesh.example.com). Requires port 80 reachable for HTTP-01 challenge.")
	autocertCache := flag.String("autocert-cache", "", "directory for autocert cert cache (default: <data-dir>/autocert)")
	dataDir := flag.String("data-dir", "", "override data directory (default: platform-specific user data dir)")
	flag.Parse()

	// Resolve data directory
	if *dataDir == "" {
		*dataDir = config.DefaultDataDir()
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
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
	chatStore := chat.New(store)
	reportsStore := reports.New(store)
	bansStore := bans.New(store)
	motdStore := motd.New(store)
	manager, err := server.NewManager(store, h, chatStore)
	if err != nil {
		log.Fatalf("init server manager: %v", err)
	}

	// Build router.
	router := api.NewRouter(store, manager, h, reportsStore, bansStore, chatStore, motdStore, *dataDir)

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

	// TLS configuration. Three modes, in order of precedence:
	//   1. -tls-cert + -tls-key  — manual certificates
	//   2. -autocert-domain      — Let's Encrypt via golang.org/x/crypto/acme
	//   3. (none)                — plain HTTP
	//
	// Autocert requires port 80 reachable for the HTTP-01 challenge,
	// so it starts an extra server on :80 that serves only /.well-known/acme-challenge/.
	if *tlsCert != "" && *tlsKey != "" {
		// Manual cert mode — ListenAndServeTLS reads the files.
	} else if *autocertDomain != "" {
		var cacheDir string
		if *autocertCache != "" {
			cacheDir = *autocertCache
		} else {
			cacheDir = filepath.Join(*dataDir, "autocert")
		}
		// Pre-create the cache dir with safe perms. autocert.DirCache
		// does MkdirAll itself, but the resulting dir inherits the
		// process umask which on Linux is typically 022 (world-readable).
		// The cache contains certs + private keys.
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			log.Fatalf("create autocert cache: %v", err)
		}
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*autocertDomain),
			Cache:      autocert.DirCache(cacheDir),
		}
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate}
		// Serve the ACME challenge on :80 alongside the main HTTPS server.
		go func() {
			log.Printf("autocert: serving HTTP-01 challenge on :80")
			if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
				log.Printf("autocert http-01 server: %v", err)
			}
		}()
	}

	// Run server in a goroutine so we can handle shutdown signals
	serverErr := make(chan error, 1)
	go func() {
		if *tlsCert != "" && *tlsKey != "" {
			log.Printf("meshed v3 listening on https://%s (manual cert)", listen)
			serverErr <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else if *autocertDomain != "" {
			log.Printf("meshed v3 listening on https://%s (Let's Encrypt via autocert)", listen)
			// TLSConfig.GetCertificate handles cert fetching; pass empty paths.
			serverErr <- srv.ListenAndServeTLS("", "")
		} else {
			log.Printf("meshed v3 listening on http://%s (TLS not enabled)", listen)
			serverErr <- srv.ListenAndServe()
		}
	}()

	// Signal channel for graceful shutdown of all background goroutines.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Periodic cleanup job. Without this, the sessions and chat_messages
	// tables grow without bound (audit finding C1).
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		purge := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if n, err := store.Sessions().PurgeExpired(ctx); err != nil {
				log.Printf("purge expired sessions: %v", err)
			} else if n > 0 {
				log.Printf("purge expired sessions: %d rows", n)
			}
			if n, err := chatStore.PurgeOlderThan(ctx, 30*24*time.Hour); err != nil {
				log.Printf("purge old chat: %v", err)
			} else if n > 0 {
				log.Printf("purge old chat: %d rows", n)
			}
		}
		purge() // run once on startup
		for {
			select {
			case <-ticker.C:
				purge()
			case <-stop:
				return
			case <-serverErr:
				return
			}
		}
	}()

	// Wait for signal or fatal server error
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Stop child game-server processes BEFORE we stop the HTTP server.
	// Otherwise `systemctl stop meshed` orphans the game servers.
	// (audit finding #14)
	if err := manager.StopAll(ctx, 5*time.Second); err != nil {
		log.Printf("manager stop all: %v", err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	fmt.Println("meshed v3 stopped")
}
