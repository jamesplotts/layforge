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
// Image generation (design doc §6.3) can optionally be configured via
// -comfyui-url and -comfyui-workflow, pointing at a self-hosted ComfyUI
// instance and an API-format workflow the operator exported from it —
// see package imagegen for why Master never constructs a workflow
// itself. Leave both unset (today's default) to run without the
// generate_scene_image DM tool at all.
//
// A second, local-only HTTP listener serves the admin/operator settings
// panel (design doc §3.3, -admin-addr, default 127.0.0.1:8090) — see
// package admin. Campaign/Security tab changes made there apply live;
// System tab changes persist to the same SQLite database and take effect
// on the next restart, which the panel can trigger itself (a graceful
// shutdown followed by re-executing this same binary with the same
// argv — see this file's restartRequested handling in run). Note for
// systemd deployments: the default KillMode (control-group) can kill the
// freshly spawned replacement process along with this one when it exits,
// since both share the unit's cgroup — set KillMode=process (or use
// Restart=on-success and let systemd itself relaunch instead of this
// self-restart) if that matters for your deployment.
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
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/imagegen"
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
	comfyUIURL := flag.String("comfyui-url", "", "base URL of a self-hosted ComfyUI instance (design doc §6.3), e.g. http://localhost:8188. Leave empty to run without image generation (today's default) — the generate_scene_image DM tool is then simply not offered. Requires -comfyui-workflow.")
	comfyUIWorkflowPath := flag.String("comfyui-workflow", "", "path to an API-format ComfyUI workflow JSON file (exported from ComfyUI's own UI via \"Save (API Format)\"), containing the literal token %%LAYFORGE_PROMPT%% in place of the positive-prompt node's text value. Master has no way to know your checkpoint/sampler/node graph, so it never constructs a workflow itself — see package imagegen. Required if -comfyui-url is set.")
	adminAddr := flag.String("admin-addr", "127.0.0.1:8090", "address for the local-only admin/operator settings panel (design doc §3.3) — deliberately not 0.0.0.0: this listener has no login of its own, only the bind address stands between it and anyone who can reach it, so it must never be reverse-proxied or otherwise exposed off the host. Leave empty to disable the admin panel entirely.")
	adminWebDir := flag.String("admin-web-dir", defaultAdminWebDir(), "directory to serve the admin panel's web UI from, mirroring -web-dir's own reasoning; empty disables serving it (the JSON API under /api/ on -admin-addr still works, useful for headless/scripted admin access).")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *dbPath, *llmURL, *llmModel, *webDir, *roomPasswordsPath, *systemEngineAddr, *campaignPoliciesPath, *comfyUIURL, *comfyUIWorkflowPath, *adminAddr, *adminWebDir, logger); err != nil {
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

// defaultAdminWebDir mirrors defaultWebDir exactly, for the admin
// panel's own static UI directory (design doc §3.3) — an "admin-web"
// directory next to the binary rather than the current working
// directory, for the same reason defaultWebDir isn't cwd-relative.
func defaultAdminWebDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "admin-web"
	}
	return filepath.Join(filepath.Dir(exe), "admin-web")
}

// run opens the event store, starts the HTTP/WebSocket listener, and
// blocks until ctx is canceled (SIGINT/SIGTERM) or the listener fails,
// then shuts down gracefully. Split out from main so the startup/
// shutdown logic is callable from a test without invoking os.Exit.
func run(addr, dbPath, llmURL, llmModel, webDir, roomPasswordsPath, systemEngineAddr, campaignPoliciesPath, comfyUIURL, comfyUIWorkflowPath, adminAddr, adminWebDir string, logger *slog.Logger) error {
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

	// restartRequested is nil (every case reading it in this function's
	// final select blocks forever, matching Go's nil-channel semantics)
	// unless -admin-addr enables the admin panel below — see that block.
	var restartRequested chan struct{}

	// The admin panel (design doc §3.3) layers SQLite-backed, live-editable
	// versions of authProvider/policyProvider on top of whichever of them
	// was just constructed above (or nil) as a fallback — wrapping happens
	// here, before either is handed to server.New, so every caller
	// downstream just sees "the" auth/policy provider and doesn't need to
	// know an admin panel exists. Skipped entirely when -admin-addr is
	// empty: authProvider/policyProvider stay exactly what they were built
	// as above, so a self-hoster not using this feature sees no behavior
	// change at all.
	var adminServer *admin.Server
	if adminAddr != "" {
		authProvider = admin.NewAuthProvider(events, authProvider)
		policyProvider = admin.NewPolicyProvider(events, policyProvider)
		restartRequested = make(chan struct{}, 1)
		systemSeed := map[string]string{
			admin.SystemKeyAddr:             addr,
			admin.SystemKeyLLMURL:           llmURL,
			admin.SystemKeyLLMModel:         llmModel,
			admin.SystemKeySystemEngineAddr: systemEngineAddr,
			admin.SystemKeyComfyUIURL:       comfyUIURL,
			admin.SystemKeyComfyUIWorkflow:  comfyUIWorkflowPath,
		}
		adminServer = admin.New(logger, events, adminWebDir, adminAddr, systemSeed, restartRequested)
	}

	// imageGenProvider stays nil (no image generation, the
	// generate_scene_image DM tool simply isn't offered) unless
	// -comfyui-url is set — same opt-in reasoning as every other
	// optional dependency above. Verified live against a real running
	// ComfyUI instance (design doc §6.3) — see package imagegen's doc
	// comment.
	var imageGenProvider imagegen.Provider
	if comfyUIURL != "" {
		if comfyUIWorkflowPath == "" {
			return errors.New("-comfyui-url requires -comfyui-workflow (an API-format ComfyUI workflow JSON file)")
		}
		workflowTemplate, err := os.ReadFile(comfyUIWorkflowPath)
		if err != nil {
			return fmt.Errorf("reading -comfyui-workflow file: %w", err)
		}
		provider, err := imagegen.NewComfyUIProvider(comfyUIURL, string(workflowTemplate))
		if err != nil {
			return fmt.Errorf("configuring ComfyUI image generation: %w", err)
		}
		imageGenProvider = provider
		logger.Info("image generation enabled", "comfyui_url", comfyUIURL, "workflow", comfyUIWorkflowPath)
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
	mux.Handle("/ws", server.New(logger, events, llmProvider, llmModel, authProvider, systemEngineClient, events, policyProvider, imageGenProvider).Handler())

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

	// adminHTTPServer stays nil unless adminServer was constructed above
	// (-admin-addr non-empty) — a second, independent *http.Server bound
	// to a different address, not a route mounted on httpServer's own
	// mux, so a public listener misconfiguration can never accidentally
	// expose this one (design doc §3.3).
	var adminHTTPServer *http.Server
	if adminServer != nil {
		adminHTTPServer = &http.Server{
			Addr:    adminAddr,
			Handler: adminServer.Handler(),
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("master listening", "addr", addr)
		serveErr <- httpServer.ListenAndServe()
	}()
	if adminHTTPServer != nil {
		go func() {
			logger.Info("admin panel listening", "addr", adminAddr)
			// Not fed into serveErr: the admin panel is a convenience,
			// same as -web-dir — a failure to bind it (e.g. the port's
			// already in use) shouldn't take down the player-facing
			// listener that's Master's actual job.
			if err := adminHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("admin panel listener stopped", "error", err)
			}
		}()
	}

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
		if adminHTTPServer != nil {
			if err := adminHTTPServer.Shutdown(shutdownCtx); err != nil {
				logger.Warn("shutting down admin listener", "error", err)
			}
		}
		return httpServer.Shutdown(shutdownCtx)
	case <-restartRequested:
		// design doc §3.3: a System-tab settings change was just
		// persisted (by adminServer's own restart handler) and needs a
		// fresh process to take effect — every such setting is wired
		// into a long-lived client/listener exactly once, above. Spawn a
		// replacement with the same argv (so it re-reads the same flags
		// plus whatever was just saved to the settings DB) and exit this
		// one cleanly, rather than syscall.Exec-style image replacement,
		// which would skip this function's deferred DB/gRPC cleanup and
		// has no real equivalent on Windows.
		logger.Info("restarting for a settings change")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if adminHTTPServer != nil {
			if err := adminHTTPServer.Shutdown(shutdownCtx); err != nil {
				logger.Warn("shutting down admin listener for restart", "error", err)
			}
		}
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("shutting down main listener for restart", "error", err)
		}
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("restarting: resolving executable path: %w", err)
		}
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("restarting: spawning replacement process: %w", err)
		}
		logger.Info("spawned replacement process", "pid", cmd.Process.Pid)
		return nil
	}
}
