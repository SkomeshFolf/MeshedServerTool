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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

// version is set at build time via -ldflags:
//
//	go build -ldflags '-X main.version=v3.0.0' ./cmd/meshed
//
// Defaults to "dev" for local builds. Exposed via /healthz so
// orchestrators and ops staff can confirm what's running.
var version = "dev"

func main() {
	addr := flag.String("addr", "", "listen address (overrides config; e.g. :5000 or 127.0.0.1:5000)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate (enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "path to TLS private key (enables HTTPS)")
	autocertDomain := flag.String("autocert-domain", "", "domain for Let's Encrypt autocert (e.g. mesh.example.com). Requires port 80 reachable for HTTP-01 challenge.")
	autocertCache := flag.String("autocert-cache", "", "directory for autocert cert cache (default: <data-dir>/autocert)")
	dataDir := flag.String("data-dir", "", "override data directory (default: platform-specific user data dir)")
	trustedProxies := flag.String("trusted-proxies", "", "comma-separated CIDR list of upstream proxies whose X-Forwarded-For header is honored when stamping session IPs (e.g. '127.0.0.1/32,10.0.0.0/8'). Default: empty (never trust XFF).")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error. Lower levels are noisier.")
	installRoot := flag.String("install-root", "", "base directory under which server install_dir must live (e.g. /opt/servers). Required in production. Default: empty (reject all server create/update).")
	allowedBinRoots := flag.String("allowed-bin-roots", "", "comma-separated extra absolute path prefixes that server executable may live under (in addition to /bin,/sbin,/usr/bin,/usr/sbin,/usr/local/bin). E.g. '/opt/scpsl,/srv/games'. Default: empty.")
	allowArbitraryExe := flag.Bool("allow-arbitrary-executable", false, "DANGEROUS: disable the executable allowlist (CRIT-1). Any path in args.executable will be accepted. Intended for tests only.")
	corsOrigins := flag.String("cors-allowed-origins", "", "comma-separated list of origins allowed to make cross-origin requests (CORS). Use '*' to allow any origin (insecure — dev only). Default: empty (no CORS headers; browser blocks cross-origin).")
	enableSecurityHeaders := flag.Bool("security-headers", true, "set standard security response headers (HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, CSP). Disable only for debugging.")
	cookieSecureForce := flag.String("cookie-secure", "auto", "session cookie Secure flag: 'auto' (Secure when r.TLS or XFF-Proto=https from a trusted proxy), 'always' (force Secure — recommended for production), 'never' (force off — HTTP local testing only). Default: 'auto'.")
	flag.Parse()

	// Configure structured logging. We route the stdlib `log` package
	// through slog so the 70+ existing log.Printf callsites work
	// unchanged, and any new code can use slog directly. (audit M2)
	var lvl slog.Level
	switch strings.ToLower(*logLevel) {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		log.Fatalf("invalid -log-level %q (want debug|info|warn|error)", *logLevel)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	}).WithAttrs([]slog.Attr{slog.String("version", version)})))
	log.SetOutput(slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo).Writer())
	log.SetFlags(0) // slog already adds time

	// Resolve data directory
	if *dataDir == "" {
		*dataDir = config.DefaultDataDir()
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	slog.Info("data dir resolved", "path", *dataDir)

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
	var trustedCIDRs []string
	if *trustedProxies != "" {
		for _, c := range strings.Split(*trustedProxies, ",") {
			if c = strings.TrimSpace(c); c != "" {
				trustedCIDRs = append(trustedCIDRs, c)
			}
		}
	}
	var allowedBinList []string
	if *allowedBinRoots != "" {
		for _, p := range strings.Split(*allowedBinRoots, ",") {
			if p = strings.TrimSpace(p); p != "" {
				allowedBinList = append(allowedBinList, p)
			}
		}
	}
	var corsList []string
	if *corsOrigins != "" {
		for _, o := range strings.Split(*corsOrigins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				corsList = append(corsList, o)
			}
		}
	}
	var cookieSecurePtr *bool
	switch strings.ToLower(strings.TrimSpace(*cookieSecureForce)) {
	case "always", "true", "yes", "on", "1":
		t := true
		cookieSecurePtr = &t
	case "never", "false", "no", "off", "0":
		f := false
		cookieSecurePtr = &f
	case "auto", "":
		// leave nil — auth package falls back to r.TLS / hook
	default:
		log.Printf("warning: --cookie-secure=%q is not recognized; expected auto|always|never. Falling back to auto.", *cookieSecureForce)
	}
	routerOpts := api.RouterOptions{
		InstallRoot:              *installRoot,
		AllowedBinRoots:          allowedBinList,
		AllowArbitraryExecutable: *allowArbitraryExe,
		CorsAllowedOrigins:       corsList,
		EnableSecurityHeaders:    *enableSecurityHeaders,
		CookieSecure:             cookieSecurePtr,
	}
	if *installRoot == "" {
		log.Printf("warning: --install-root is not set; server create/update will reject all install_dir values (CRIT-2). Set --install-root in production.")
	}
	router := api.NewRouter(store, manager, h, reportsStore, bansStore, chatStore, motdStore, *dataDir, trustedCIDRs, version, routerOpts)

	// Effective listen address
	listen := *addr
	if listen == "" {
		listen = ":5000"
	}

	srv := &http.Server{
		Addr:              listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		// (MED-2) Slowloris/slow-read hardening for the request body.
		// We bound ReadTimeout (covers both headers+body); we deliberately
		// leave WriteTimeout at 0 because the WebSocket endpoint is a
		// long-lived connection and would be killed at 30s. Per-route
		// timeouts can be added via http.TimeoutHandler if needed.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
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
