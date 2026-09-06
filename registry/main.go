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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

func run(addr, webDir string, ttl, sweepInterval time.Duration, logger *slog.Logger) error {
	store := lobby.NewStore()

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", lobby.NewHandler(store, ttl))

	if webDir != "" {
		if info, statErr := os.Stat(webDir); statErr != nil || !info.IsDir() {
			logger.Warn("web directory not found, not serving it", "web_dir", webDir, "error", statErr)
		} else {
			mux.Handle("/", http.FileServer(http.Dir(webDir)))
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
			}
		}
	}()

	httpServer := &http.Server{Addr: addr, Handler: mux}
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
