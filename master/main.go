// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Command master runs Layforge's Master process: the only node holding
// LLM provider credentials, and the WebSocket endpoint Slave clients
// connect to. See docs/design.md §3.
//
// The client-handshake WebSocket endpoint, safety.flag broadcast,
// campaign history paging, and the narrative-transform pipeline's fast
// pass (package server) are wired up so far — session orchestration
// beyond that, the turn-order state machine, authoritative dice, the
// narrative-transform pipeline's slow pass, and tool-use dispatch are
// all still to come (docs/design.md §11).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "address for the WebSocket/HTTP listener")
	dbPath := flag.String("db", "layforge.db", "path to the SQLite event-log database (design doc §10's zero-config default); use :memory: to disable persistence across restarts")
	llmURL := flag.String("llm-url", "", "base URL of an Ollama server for the narrative-transform pipeline (design doc §7), e.g. http://192.168.1.56:11434; leave empty to disable narrative rendering")
	llmModel := flag.String("llm-model", "qwen3.8:27b", "Ollama model tag to use for narrative rendering; ignored if -llm-url is empty")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *dbPath, *llmURL, *llmModel, logger); err != nil {
		logger.Error("master exited with error", "error", err)
		os.Exit(1)
	}
}

// run opens the event store, starts the HTTP/WebSocket listener, and
// blocks until ctx is canceled (SIGINT/SIGTERM) or the listener fails,
// then shuts down gracefully. Split out from main so the startup/
// shutdown logic is callable from a test without invoking os.Exit.
func run(addr, dbPath, llmURL, llmModel string, logger *slog.Logger) error {
	events, err := store.OpenSQLiteEventStore(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := events.Close(); err != nil {
			logger.Warn("closing event store", "error", err)
		}
	}()
	logger.Info("event store opened", "path", dbPath)

	// llmProvider stays nil (narrative rendering disabled) unless -llm-url
	// is set — Ollama isn't a zero-config default the way SQLite is, so
	// self-hosters without one configured get a clean "unavailable"
	// system.error on narrative.player_input rather than Master trying
	// (and failing) to reach some default host that isn't theirs.
	var llmProvider llm.Provider
	if llmURL != "" {
		llmProvider = llm.NewOllamaProvider(llmURL, nil)
		logger.Info("narrative rendering enabled", "llm_url", llmURL, "model", llmModel)
	} else {
		logger.Info("narrative rendering disabled (no -llm-url configured)")
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", server.New(logger, events, llmProvider, llmModel).Handler())

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("master listening", "addr", addr)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
