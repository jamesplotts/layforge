// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Command master runs Layforge's Master process: the only node holding
// LLM provider credentials, and the WebSocket endpoint Slave clients
// connect to. See docs/design.md §3.
//
// By default it also serves the reference V1 web client (design doc §4)
// from a "web" directory next to this binary — see defaultWebDir. That
// directory is plain files on disk, not embedded into the binary, so a
// self-hoster (or a table running their own instance) can restyle the
// interface — swap style.css, fork index.html/app.js — without touching
// Go at all. Serving it doesn't compromise the protocol's own openness
// (design doc §4: "third-party clients are legitimate first-class
// consumers"): anything Master hands out at / is just a convenience
// default, and any other client is equally free to connect to /ws
// directly.
//
// Joining a campaign can optionally require a password (-room-passwords,
// design doc §6.6's room-code auth provider — see package auth). That
// same seam is where a future Discord-OAuth-backed provider is meant to
// plug in, per that section's reference design; nothing OAuth-specific
// exists yet, only the interface it would satisfy.
//
// A System Engine gRPC sidecar (design doc §6.1, e.g. OpenCombatEngine's
// GrpcSidecar) can optionally be configured via -system-engine-addr —
// see package systemengine for the client and this file's connectivity
// check at startup, and package server for what calls it (character
// import/validation, authoritative dice, the DM tool-use API).
//
// A campaign's PvP policy and maturity-tier prompt constraint (design
// doc §9.1, §9.5) can optionally be configured via -campaign-policies —
// see package policy. A campaign not listed there (or the flag left
// unset entirely) gets the strictest safe default, not an open one; see
// policy.Default's doc comment for why that differs from
// -room-passwords' own unconfigured-is-open default just above.
//
// See package server's own doc comment for what's implemented so far by
// protocol area, and docs/design.md §11 for the overall roadmap.
package main

import (
	"context"
	"encoding/json"
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

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemengine"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func main() {
	addr := flag.String("addr", ":8080", "address for the WebSocket/HTTP listener")
	dbPath := flag.String("db", "layforge.db", "path to the SQLite event-log database (design doc §10's zero-config default); use :memory: to disable persistence across restarts")
	llmURL := flag.String("llm-url", "", "base URL of an Ollama server for the narrative-transform pipeline (design doc §7), e.g. http://192.168.1.56:11434; leave empty to disable narrative rendering")
	llmModel := flag.String("llm-model", "qwen3.8:27b", "Ollama model tag to use for narrative rendering; ignored if -llm-url is empty")
	webDir := flag.String("web-dir", defaultWebDir(), "directory to serve at / — the reference web client (design doc §4). Defaults to a \"web\" directory next to this binary, so a self-hoster can restyle it in place (see the package doc comment). Pass a different path to point at another copy (e.g. master/web itself, when iterating on the client via 'go run .' from within master/), or an empty string to disable serving it.")
	roomPasswordsPath := flag.String("room-passwords", "", "path to a JSON file mapping campaign_id to a required join password (design doc §6.6's room-code auth provider), e.g. {\"my-campaign\": \"hunter2\"}. A campaign not listed is open to anyone. Leave empty to require no password anywhere (today's default).")
	systemEngineAddr := flag.String("system-engine-addr", "", "host:port of a System Engine gRPC sidecar (design doc §6.1), e.g. localhost:5265 for a locally running OpenCombatEngine.GrpcSidecar. Leave empty to run without one (today's default) — nothing calls it yet, since dice/rules dispatch is still design doc §11 future work.")
	campaignPoliciesPath := flag.String("campaign-policies", "", "path to a JSON file mapping campaign_id to governance settings (design doc §9.1's PvP policy, §9.5's maturity-tier prompt constraint), e.g. {\"my-campaign\": {\"pvp_policy\": \"pvp_with_consent\", \"pvp_consent\": [\"player-a\"], \"maturity_tier_prompt\": \"Keep content suitable for all ages.\"}}. pvp_policy is one of pve_only, pvp_allowed, pvp_with_consent. A campaign not listed (or this flag left empty, today's default) gets pve_only with no maturity constraint — the strictest safe default.")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *dbPath, *llmURL, *llmModel, *webDir, *roomPasswordsPath, *systemEngineAddr, *campaignPoliciesPath, logger); err != nil {
		logger.Error("master exited with error", "error", err)
		os.Exit(1)
	}
}

// loadRoomPasswords reads and parses the JSON file at path into a
// campaign_id -> password map. Any error here (missing file, malformed
// JSON) is treated as fatal by the caller rather than falling back to
// "no passwords configured" — a self-hoster who explicitly asked for
// this file to be loaded should never silently end up with an
// unprotected campaign because of a typo.
func loadRoomPasswords(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading room-passwords file: %w", err)
	}
	var passwords map[string]string
	if err := json.Unmarshal(data, &passwords); err != nil {
		return nil, fmt.Errorf("parsing room-passwords file: %w", err)
	}
	return passwords, nil
}

// rawCampaignPolicy is the JSON shape -campaign-policies' file uses per
// campaign — a thin, string-keyed mirror of policy.CampaignPolicy so the
// package itself doesn't need JSON struct tags on its own domain type.
type rawCampaignPolicy struct {
	PvPPolicy          string   `json:"pvp_policy"`
	PvPConsent         []string `json:"pvp_consent,omitempty"`
	MaturityTierPrompt string   `json:"maturity_tier_prompt,omitempty"`
}

// loadCampaignPolicies reads and parses the JSON file at path into a
// campaign_id -> policy.CampaignPolicy map (design doc §9.1, §9.5). Same
// "fail startup outright rather than silently misconfigure" behavior as
// loadRoomPasswords, plus validation loadRoomPasswords doesn't need: an
// unrecognized pvp_policy value is fatal here too, since a self-hoster
// who mistyped it should never end up with a silently-wrong PvP setting.
// An omitted pvp_policy defaults to policy.PvPPolicyPveOnly, the same
// safe default policy.Default() applies to an unlisted campaign.
func loadCampaignPolicies(path string) (map[string]policy.CampaignPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading campaign-policies file: %w", err)
	}
	var raw map[string]rawCampaignPolicy
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing campaign-policies file: %w", err)
	}
	policies := make(map[string]policy.CampaignPolicy, len(raw))
	for campaignID, p := range raw {
		pvp := policy.PvPPolicy(p.PvPPolicy)
		switch {
		case pvp == policy.PvPPolicyUnspecified:
			pvp = policy.PvPPolicyPveOnly
		case !pvp.IsValid():
			return nil, fmt.Errorf("campaign-policies file: campaign %q: invalid pvp_policy %q (want pve_only, pvp_allowed, or pvp_with_consent)", campaignID, p.PvPPolicy)
		}
		policies[campaignID] = policy.CampaignPolicy{
			PvPPolicy:          pvp,
			PvPConsent:         p.PvPConsent,
			MaturityTierPrompt: p.MaturityTierPrompt,
		}
	}
	return policies, nil
}

// defaultWebDir returns the path to the reference web client that ships
// alongside this binary: a "web" directory next to the executable
// itself, not relative to the current working directory — so `./master`
// serves it correctly regardless of where it's launched from, as long as
// web/ travels with the binary. Falls back to a cwd-relative "web" if the
// executable's own path can't be determined, which should only happen in
// unusual environments (e.g. some minimal containers).
func defaultWebDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "web"
	}
	return filepath.Join(filepath.Dir(exe), "web")
}

// run opens the event store, starts the HTTP/WebSocket listener, and
// blocks until ctx is canceled (SIGINT/SIGTERM) or the listener fails,
// then shuts down gracefully. Split out from main so the startup/
// shutdown logic is callable from a test without invoking os.Exit.
func run(addr, dbPath, llmURL, llmModel, webDir, roomPasswordsPath, systemEngineAddr, campaignPoliciesPath string, logger *slog.Logger) error {
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

	// authProvider stays nil (every campaign open to anyone) unless
	// -room-passwords is set — matches every existing campaign's
	// behavior today; opting a campaign into a password is something a
	// self-hoster does deliberately, not a new default forced on them.
	var authProvider auth.Provider
	if roomPasswordsPath != "" {
		passwords, err := loadRoomPasswords(roomPasswordsPath)
		if err != nil {
			return err
		}
		authProvider = auth.NewRoomPasswordProvider(passwords)
		logger.Info("room passwords loaded", "path", roomPasswordsPath, "campaign_count", len(passwords))
	}

	// policyProvider stays nil (every campaign gets policy.Default() —
	// pve_only, no maturity constraint) unless -campaign-policies is set,
	// same opt-in-per-self-hoster reasoning as authProvider above, except
	// the *unconfigured* default itself is deliberately restrictive
	// rather than permissive — see policy.Default's doc comment for why
	// PvP specifically shouldn't default open the way join auth does.
	var policyProvider policy.Provider
	if campaignPoliciesPath != "" {
		policies, err := loadCampaignPolicies(campaignPoliciesPath)
		if err != nil {
			return err
		}
		policyProvider = policy.NewJSONFileProvider(policies)
		logger.Info("campaign policies loaded", "path", campaignPoliciesPath, "campaign_count", len(policies))
	}

	// systemEngineClient stays nil (no rules-resolution or character-import
	// calls possible) unless -system-engine-addr is set. character.upload
	// is its one caller today (design doc §9.4's mechanical half — see
	// package server's importCharacter); dice/rules dispatch is still
	// design doc §11 future work.
	var systemEngineClient systemenginepb.SystemEngineClient
	if systemEngineAddr != "" {
		client, closeEngine, err := systemengine.Dial(systemEngineAddr)
		if err != nil {
			return err
		}
		defer func() {
			if err := closeEngine(); err != nil {
				logger.Warn("closing system engine connection", "error", err)
			}
		}()
		systemEngineClient = client

		// grpc-go dials lazily (see package systemengine's Dial doc), so
		// the Dial call above cannot by itself prove the sidecar is
		// actually reachable — only a real RPC can. This is a genuine
		// connectivity check, not fatal on failure: a self-hoster who
		// configured a sidecar that isn't up yet (or is still starting)
		// should still get a working Master, just without rules
		// resolution, exactly like a missing -web-dir doesn't stop
		// Master's actual job.
		checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		schema, err := systemEngineClient.GetCharacterSchema(checkCtx, &systemenginepb.GetCharacterSchemaRequest{})
		cancel()
		if err != nil {
			logger.Warn("system engine configured but unreachable", "addr", systemEngineAddr, "error", err)
		} else {
			logger.Info("system engine connected", "addr", systemEngineAddr, "schema_version", schema.SchemaVersion)
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", server.New(logger, events, llmProvider, llmModel, authProvider, systemEngineClient, events, policyProvider).Handler())

	if webDir != "" {
		if info, statErr := os.Stat(webDir); statErr != nil || !info.IsDir() {
			// Not fatal: Master's actual job (the protocol endpoint)
			// doesn't depend on this — a missing/moved web/ directory
			// just means no reference client is being served, not a
			// broken Master.
			logger.Warn("web client directory not found, not serving it", "web_dir", webDir, "error", statErr)
		} else {
			mux.Handle("/", http.FileServer(http.Dir(webDir)))
			logger.Info("serving web client", "web_dir", webDir)
		}
	}

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
