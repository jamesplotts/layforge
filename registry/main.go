// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Command registry runs layforge.org's public campaign directory: the
// JSON API package lobby implements, plus (optionally) the static
// homepage/lobby-page files at web/ — the same "serve the API and a
// plain-HTML/JS reference UI off one binary" shape master/main.go
// already uses for Master itself and its admin panel. See
// registry/README.md for what this is, how a self-hosted Master opts a
// campaign into it, and how it's actually deployed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jamesplotts/layforge/registry/internal/lobby"
)

func main() {
	addr := flag.String("addr", ":8091", "address for the HTTP listener")
	webDir := flag.String("web-dir", defaultWebDir(), "directory to serve at / — the homepage + lobby page (see web/). Defaults to a \"web\" directory next to this binary. Empty disables serving it, leaving only the JSON API under /api/v1/.")
	ttl := flag.Duration("ttl", 90*time.Second, "how long a listing stays live after its most recent heartbeat before it's excluded from GET /api/v1/listings and eventually swept — comfortably longer than any reasonable Master heartbeat interval (30s is the reference master/internal/registry client's own default), so one or two missed beats doesn't flicker a listing on and off.")
	sweepInterval := flag.Duration("sweep-interval", 30*time.Second, "how often to actually delete listings past -ttl, reclaiming memory for long-abandoned entries. Purely a memory-hygiene knob — GET /api/v1/listings already excludes expired listings regardless of whether a sweep has run yet.")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *webDir, *ttl, *sweepInterval, logger); err != nil {
		logger.Error("registry exited with error", "error", err)
		os.Exit(1)
	}
}

// Write-endpoint rate limiting: generous enough that a single operator
// running several campaigns off one IP, each heartbeating independently,
// never trips it in normal operation, while still meaningfully capping a
// tight-loop spam attempt (see lobby.IPRateLimiter's own doc comment —
// this only bounds one IP; lobby.ErrStoreFull covers a distributed
// attempt from many IPs).
const (
	writeRateLimitPerSecond = 2
	writeRateLimitBurst     = 20
	// rateLimiterIdleTimeout bounds how long a quiet IP's bucket lingers
	// before Sweep reclaims it — independent of -ttl/-sweep-interval
	// (which govern listings, not rate-limit bookkeeping).
	rateLimiterIdleTimeout = 10 * time.Minute
)

func run(addr, webDir string, ttl, sweepInterval time.Duration, logger *slog.Logger) error {
	store := lobby.NewStore()
	limiter := lobby.NewIPRateLimiter(writeRateLimitPerSecond, writeRateLimitBurst)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", rateLimitWrites(limiter, lobby.NewHandler(store, ttl)))

	if webDir != "" {
		if info, statErr := os.Stat(webDir); statErr != nil || !info.IsDir() {
			logger.Warn("web directory not found, not serving it", "web_dir", webDir, "error", statErr)
		} else {
			mux.Handle("/", revalidateStatic(http.FileServer(http.Dir(webDir))))
			logger.Info("serving lobby web UI", "web_dir", webDir)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				store.Sweep(ttl)
				limiter.Sweep(rateLimiterIdleTimeout)
			}
		}
	}()

	httpServer := &http.Server{
		Addr:    addr,
		Handler: securityHeaders(mux),
		// Slowloris/connection-exhaustion defense — the default
		// *http.Server has no timeouts at all, so a client opening many
		// connections and trickling headers/body in slowly can exhaust
		// goroutines/file descriptors with minimal bandwidth.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("registry listening", "addr", addr, "ttl", ttl)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listening: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}
	}
	<-sweepDone
	return nil
}

// securityHeaders wraps next to set a fixed set of hardening response
// headers on every response — belt-and-suspenders alongside whatever the
// Apache reverse proxy in front of this in production also sets, so
// these apply even to a direct request against this binary (local
// testing, or a self-hoster who doesn't reverse-proxy it). The site
// itself only ever loads its own same-origin style.css/app.js (no
// external CDN/font/script), so a strict default-src 'self' CSP costs
// nothing functionally.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// revalidateStatic wraps a static file handler so a browser always
// checks with the server before reusing a cached response. The site's
// HTML/CSS/JS (and the downloads) change in place at the same paths, and
// without this a browser heuristically caches them and a plain refresh
// keeps showing the old version until a hard reload. "no-cache" still
// lets the response be stored — http.FileServer answers the resulting
// conditional request with a cheap 304 whenever the file is unchanged,
// so this costs a round trip's headers, not a re-download.
func revalidateStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// rateLimitWrites wraps next to reject a non-GET request against a
// throttled IP with 429 before it ever reaches lobby's own handlers —
// GET (the public listings read) is never limited, only the write
// endpoints spam/abuse would actually target.
func rateLimitWrites(limiter *lobby.IPRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !limiter.Allow(clientIP(r)) {
			http.Error(w, "rate limit exceeded, try again shortly", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the request's real source IP for rate-limiting
// purposes. This binary is documented (registry/README.md) as meant to
// run behind a reverse proxy on a private port, never exposed directly
// to the internet — trusting X-Forwarded-For's first hop is only safe
// under that deployment shape; a self-hoster who exposes this binary
// directly without a proxy in front loses rate-limit integrity to a
// spoofed header, the same caveat that applies to trusting any proxy
// header at all.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// defaultWebDir mirrors master/main.go's own defaultWebDir exactly — a
// "web" directory next to the binary, not the current working
// directory, so this runs correctly regardless of where it's launched
// from (e.g. a systemd unit with its own WorkingDirectory).
func defaultWebDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "web"
	}
	return filepath.Join(filepath.Dir(exe), "web")
}
