// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// System-tab setting keys, as stored in store.AdminSettingsStore's
// system_settings table (design doc §3.3). Exported so main.go can build
// the seed map it passes to New from the same flag values it already
// parses, without either side hardcoding the key strings twice.
const (
	SystemKeyAddr             = "addr"
	SystemKeyLLMURL           = "llm_url"
	SystemKeyLLMModel         = "llm_model"
	SystemKeySystemEngineAddr = "system_engine_addr"
	SystemKeyComfyUIURL       = "comfyui_url"
	SystemKeyComfyUIWorkflow  = "comfyui_workflow_path"
)

// systemKeys is every recognized System-tab key, in the fixed order the
// admin UI displays them.
var systemKeys = []string{
	SystemKeyAddr,
	SystemKeyLLMURL,
	SystemKeyLLMModel,
	SystemKeySystemEngineAddr,
	SystemKeyComfyUIURL,
	SystemKeyComfyUIWorkflow,
}

// Server is design doc §3.3's admin/operator HTTP surface: a JSON API
// under /api/ (Campaign/Security tab settings, System tab settings, and
// the restart trigger) plus a static file server for the admin web UI at
// everything else — the same "/api under a mux, static files as the
// fallback handler" shape main.go already uses for the player-facing
// listener. Callers are expected to bind this Handler to a
// localhost-only *http.Server (see main.go) — Server itself does not
// enforce that; it only enforces the narrower same-origin check described
// on requireSameOrigin.
type Server struct {
	logger *slog.Logger
	store  store.AdminSettingsStore
	webDir string
	// origin is this admin listener's own "scheme://host:port", used by
	// requireSameOrigin to reject a mutating request whose Origin/Referer
	// header names a different origin — see that method's doc comment.
	origin string
	// systemSeed holds the System-tab values Master actually booted with
	// (the CLI flags), used to fill in a key GetSystemSettings has never
	// stored an override for — see handleGetSystem.
	systemSeed map[string]string
	// restartRequested is signaled once by handleRestart, after its HTTP
	// response has been flushed, to ask main.go's run() to gracefully
	// shut down and re-exec Master (design doc §3.3). Buffered by the
	// caller (main.go) with capacity 1; New does not create it, since
	// main.go's own select statement needs to hold the receiving end.
	restartRequested chan<- struct{}
}

// New creates a Server. addr is this admin listener's own bind address
// (e.g. "127.0.0.1:8090"), used only to compute the same-origin check —
// New does not itself listen on it. systemSeed should contain every key
// in systemKeys, seeded from the CLI flag values Master actually started
// with; a key GetSystemSettings has never stored an override for falls
// back to this map (see handleGetSystem). restartRequested is the
// send-only end of a channel main.go's run() selects on.
func New(logger *slog.Logger, s store.AdminSettingsStore, webDir, addr string, systemSeed map[string]string, restartRequested chan<- struct{}) *Server {
	return &Server{
		logger:           logger,
		store:            s,
		webDir:           webDir,
		origin:           "http://" + addr,
		systemSeed:       systemSeed,
		restartRequested: restartRequested,
	}
}

// Handler returns the admin HTTP handler: the JSON API under /api/, and
// (if webDir is non-empty) the admin web UI's static files for
// everything else — mirroring main.go's own /ws-plus-static-fallback
// pattern for the player-facing listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/campaigns", s.handleListCampaigns)
	mux.HandleFunc("POST /api/campaigns", s.requireSameOrigin(s.handleCreateCampaign))
	mux.HandleFunc("PUT /api/campaigns/{id}/archive", s.requireSameOrigin(s.handlePutCampaignArchived))
	mux.HandleFunc("DELETE /api/campaigns/{id}", s.requireSameOrigin(s.handleDeleteCampaign))
	mux.HandleFunc("GET /api/campaigns/{id}/policy", s.handleGetCampaignPolicy)
	mux.HandleFunc("PUT /api/campaigns/{id}/policy", s.requireSameOrigin(s.handlePutCampaignPolicy))
	mux.HandleFunc("GET /api/campaigns/{id}/security", s.handleGetCampaignSecurity)
	mux.HandleFunc("PUT /api/campaigns/{id}/security", s.requireSameOrigin(s.handlePutCampaignSecurity))
	mux.HandleFunc("GET /api/system", s.handleGetSystem)
	mux.HandleFunc("PUT /api/system", s.requireSameOrigin(s.handlePutSystem))
	mux.HandleFunc("POST /api/system/restart", s.requireSameOrigin(s.handleRestart))
	mux.HandleFunc("GET /api/health", s.handleHealth)

	if s.webDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(s.webDir)))
	}
	return mux
}

// requireSameOrigin wraps a mutating handler to reject a request whose
// Origin (falling back to Referer) header names something other than
// this admin listener's own origin — see design doc §3.3: the bind
// address is the real access boundary, but without this a malicious page
// open in the same browser on the same machine could still issue a
// cross-origin fetch() at this port and change settings the operator
// never asked to change. A request with neither header (any non-browser
// client, e.g. curl) is allowed through — this check defends against a
// browser-mediated drive-by, not against local shell access, which
// already implies full trust per §3.3.
func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin != "" && !strings.HasPrefix(origin, s.origin) {
			s.writeErrorMsg(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next(w, r)
	}
}

// campaignPolicyDTO is the Campaign tab's wire shape — the same fields
// store.CampaignSettings carries for policy, minus RoomPassword (that's
// campaignSecurityDTO's concern instead, per the tab split).
type campaignPolicyDTO struct {
	PvPPolicy               string   `json:"pvp_policy"`
	PvPConsent              []string `json:"pvp_consent"`
	MaturityTierPrompt      string   `json:"maturity_tier_prompt"`
	ImageMaturityTierPrompt string   `json:"image_maturity_tier_prompt"`
}

// campaignSecurityDTO is the Security tab's wire shape. An empty
// RoomPassword means "no password required" — see
// store.CampaignSettings.RoomPassword's doc comment.
type campaignSecurityDTO struct {
	RoomPassword string `json:"room_password"`
}

// systemSettingsDTO is the System tab's wire shape — one field per
// systemKeys entry.
type systemSettingsDTO struct {
	Addr                string `json:"addr"`
	LLMURL              string `json:"llm_url"`
	LLMModel            string `json:"llm_model"`
	SystemEngineAddr    string `json:"system_engine_addr"`
	ComfyUIURL          string `json:"comfyui_url"`
	ComfyUIWorkflowPath string `json:"comfyui_workflow_path"`
}

func (d systemSettingsDTO) toMap() map[string]string {
	return map[string]string{
		SystemKeyAddr:             d.Addr,
		SystemKeyLLMURL:           d.LLMURL,
		SystemKeyLLMModel:         d.LLMModel,
		SystemKeySystemEngineAddr: d.SystemEngineAddr,
		SystemKeyComfyUIURL:       d.ComfyUIURL,
		SystemKeyComfyUIWorkflow:  d.ComfyUIWorkflowPath,
	}
}

func systemSettingsDTOFromMap(m map[string]string) systemSettingsDTO {
	return systemSettingsDTO{
		Addr:                m[SystemKeyAddr],
		LLMURL:              m[SystemKeyLLMURL],
		LLMModel:            m[SystemKeyLLMModel],
		SystemEngineAddr:    m[SystemKeySystemEngineAddr],
		ComfyUIURL:          m[SystemKeyComfyUIURL],
		ComfyUIWorkflowPath: m[SystemKeyComfyUIWorkflow],
	}
}

// campaignSummaryDTO is one row of the campaign list's wire shape —
// store.CampaignSummary's fields, JSON-cased, with LastActiveAt omitted
// (empty string) rather than serialized as Go's zero time.Time when no
// activity has happened yet.
type campaignSummaryDTO struct {
	CampaignID   string `json:"campaign_id"`
	DisplayName  string `json:"display_name"`
	PartyCount   int    `json:"party_count"`
	LastActiveAt string `json:"last_active_at,omitempty"`
	Archived     bool   `json:"archived"`
}

func (s *Server) handleListCampaigns(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.store.ListCampaignSummaries(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	dtos := make([]campaignSummaryDTO, len(summaries))
	for i, summary := range summaries {
		dto := campaignSummaryDTO{
			CampaignID:  summary.CampaignID,
			DisplayName: summary.DisplayName,
			PartyCount:  summary.PartyCount,
			Archived:    summary.Archived,
		}
		if !summary.LastActiveAt.IsZero() {
			dto.LastActiveAt = summary.LastActiveAt.UTC().Format(time.RFC3339)
		}
		dtos[i] = dto
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"campaigns": dtos})
}

// createCampaignDTO is POST /api/campaigns' request body.
type createCampaignDTO struct {
	CampaignID  string `json:"campaign_id"`
	DisplayName string `json:"display_name"`
}

// handleCreateCampaign is the admin panel's "create/name a campaign"
// action (see store.AdminSettingsStore.SaveCampaignMeta's doc comment) —
// upserts a display name against campaign_id without touching how it's
// joined, played, or governed. Naming an already-active campaign (one
// with real characters/events already) just attaches a label; it never
// resets or clears anything real.
func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var dto createCampaignDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if dto.CampaignID == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign_id is required")
		return
	}
	if err := s.store.SaveCampaignMeta(r.Context(), dto.CampaignID, dto.DisplayName); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

type campaignArchivedDTO struct {
	Archived bool `json:"archived"`
}

// handlePutCampaignArchived toggles a campaign's archived display-filter
// flag — see store.AdminSettingsStore.SetCampaignArchived's doc comment:
// this never affects whether the campaign can be joined or played over
// the WS endpoint, purely what the admin panel's own list shows.
func (s *Server) handlePutCampaignArchived(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	var dto campaignArchivedDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := s.store.SetCampaignArchived(r.Context(), id, dto.Archived); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

// handleDeleteCampaign permanently removes campaignID and everything
// referencing it — see store.AdminSettingsStore.DeleteCampaign's own doc
// comment for exactly what's deleted and why archiving first is a real,
// store-enforced precondition, not just something this handler checks.
// ErrCampaignNotArchived is surfaced as 400 (a real, expected rejection
// this handler anticipates), not the generic 500 writeError otherwise
// returns for an unexpected store failure.
func (s *Server) handleDeleteCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	err := s.store.DeleteCampaign(r.Context(), id)
	if errors.Is(err, store.ErrCampaignNotArchived) {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign must be archived before it can be deleted")
		return
	}
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleGetCampaignPolicy(w http.ResponseWriter, r *http.Request) {
	settings, _, err := s.store.GetCampaignSettings(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, campaignPolicyDTO{
		PvPPolicy:               settings.PvPPolicy,
		PvPConsent:              settings.PvPConsent,
		MaturityTierPrompt:      settings.MaturityTierPrompt,
		ImageMaturityTierPrompt: settings.ImageMaturityTierPrompt,
	})
}

func (s *Server) handlePutCampaignPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	var dto campaignPolicyDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if dto.PvPPolicy != "" && !policy.PvPPolicy(dto.PvPPolicy).IsValid() {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid pvp_policy (want pve_only, pvp_allowed, or pvp_with_consent)")
		return
	}

	current, _, err := s.store.GetCampaignSettings(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	current.PvPPolicy = dto.PvPPolicy
	current.PvPConsent = dto.PvPConsent
	current.MaturityTierPrompt = dto.MaturityTierPrompt
	current.ImageMaturityTierPrompt = dto.ImageMaturityTierPrompt
	if err := s.store.SaveCampaignSettings(r.Context(), id, current); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleGetCampaignSecurity(w http.ResponseWriter, r *http.Request) {
	settings, _, err := s.store.GetCampaignSettings(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, campaignSecurityDTO{RoomPassword: settings.RoomPassword})
}

func (s *Server) handlePutCampaignSecurity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	var dto campaignSecurityDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	current, _, err := s.store.GetCampaignSettings(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	current.RoomPassword = dto.RoomPassword
	if err := s.store.SaveCampaignSettings(r.Context(), id, current); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	stored, err := s.store.GetSystemSettings(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	effective := make(map[string]string, len(systemKeys))
	for _, key := range systemKeys {
		effective[key] = s.systemSeed[key]
	}
	for key, value := range stored {
		effective[key] = value
	}
	s.writeJSON(w, http.StatusOK, systemSettingsDTOFromMap(effective))
}

func (s *Server) saveSystemSettings(ctx context.Context, dto systemSettingsDTO) error {
	return s.store.SaveSystemSettings(ctx, dto.toMap())
}

func (s *Server) handlePutSystem(w http.ResponseWriter, r *http.Request) {
	var dto systemSettingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := s.saveSystemSettings(r.Context(), dto); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

// restartSignalDelay gives handleRestart's HTTP response time to flush to
// the client before the process starts shutting down — long enough for a
// same-machine loopback round trip under any reasonable load, short
// enough not to feel like a hang to whoever clicked "Save & Restart."
const restartSignalDelay = 200 * time.Millisecond

// handleRestart optionally persists a System-tab settings body (the
// "Save & Restart" case; an empty body just restarts with whatever was
// already saved), responds 202 once that's durable, then signals
// restartRequested after a short delay so the response above has time to
// reach the client first — see restartSignalDelay.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	var dto systemSettingsDTO
	err := json.NewDecoder(r.Body).Decode(&dto)
	switch {
	case err == nil:
		if err := s.saveSystemSettings(r.Context(), dto); err != nil {
			s.writeError(w, err)
			return
		}
	case errors.Is(err, io.EOF):
		// No body — restart with whatever's already saved.
	default:
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(restartSignalDelay)
		select {
		case s.restartRequested <- struct{}{}:
		default:
			// Already signaled (e.g. a second restart click before the
			// first took effect) — main.go's run() only needs one.
		}
	}()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("admin: failed to encode JSON response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	s.logger.Warn("admin: request failed", "error", err)
	s.writeErrorMsg(w, http.StatusInternalServerError, err.Error())
}

func (s *Server) writeErrorMsg(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
