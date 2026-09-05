// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
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
	logger       *slog.Logger
	store        store.AdminSettingsStore
	campaignPack store.CampaignPackStore
	// pregens persists Host/DM-authored pregenerated characters (design
	// doc §9.4's join-time "pick a pregen" option) — nil means the
	// Pregens tab's endpoints reject with a real "not configured" error,
	// the same nil-disables-the-feature pattern as campaignPack.
	pregens store.PregenStore
	// characters persists uploaded/imported characters (design doc §9.4)
	// — nil means the Character Review tab's endpoints reject with a
	// real "not configured" error, same as pregens.
	characters store.CharacterStore
	// hub is the same connection registry package server's live /ws
	// listener uses, shared via main.go so an admin approve/reject
	// action can push character.review_result to a live player
	// immediately (handleReviewCharacter) rather than only updating the
	// database. nil means that push is silently skipped — the status
	// still changes, the player just finds out on their next
	// character.get/reconnect instead of live.
	hub    *session.Hub
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
func New(logger *slog.Logger, s store.AdminSettingsStore, campaignPack store.CampaignPackStore, pregens store.PregenStore, characters store.CharacterStore, webDir, addr string, systemSeed map[string]string, restartRequested chan<- struct{}, hub *session.Hub) *Server {
	return &Server{
		logger:           logger,
		store:            s,
		campaignPack:     campaignPack,
		pregens:          pregens,
		characters:       characters,
		hub:              hub,
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
	mux.HandleFunc("GET /api/campaigns/{id}/pack", s.handleGetCampaignPack)
	mux.HandleFunc("PUT /api/campaigns/{id}/pack", s.requireSameOrigin(s.handlePutCampaignPack))
	mux.HandleFunc("GET /api/campaigns/{id}/pregens", s.handleListPregens)
	mux.HandleFunc("PUT /api/campaigns/{id}/pregens", s.requireSameOrigin(s.handlePutPregen))
	mux.HandleFunc("DELETE /api/campaigns/{id}/pregens/{pregenId}", s.requireSameOrigin(s.handleDeletePregen))
	mux.HandleFunc("GET /api/campaigns/{id}/characters", s.handleListCharacters)
	mux.HandleFunc("PUT /api/campaigns/{id}/characters/{characterId}/review", s.requireSameOrigin(s.handleReviewCharacter))
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
	// PriceMultiplier mirrors store.CampaignSettings.PriceMultiplier — 0
	// means "not set", resolved to 1.0 by
	// policy.CampaignPolicy.EffectivePriceMultiplier.
	PriceMultiplier float64 `json:"price_multiplier"`
	// MinLevel/MaxLevel mirror store.CampaignSettings's own fields of the
	// same name (design doc §9.4's character-import review flow) — 0 in
	// either means "no bound in that direction".
	MinLevel int `json:"min_level"`
	MaxLevel int `json:"max_level"`
}

// campaignSecurityDTO is the Security tab's wire shape. An empty
// RoomPassword means "no password required" — see
// store.CampaignSettings.RoomPassword's doc comment.
type campaignSecurityDTO struct {
	RoomPassword string `json:"room_password"`
}

// campaignPackDTO is the pack-binding tab's wire shape. Both fields
// empty means no pack is bound — the same "absence means unset" pattern
// campaignPolicyDTO/campaignSecurityDTO already use.
type campaignPackDTO struct {
	PackDir string `json:"pack_dir"`
	PackID  string `json:"pack_id"`
}

// pregenDTO is the Pregens tab's wire shape (design doc §9.4) — ID is
// Host-chosen (e.g. "bram-fighter"), not server-generated, since it's
// what a player's join-time character.creation_prompt.choices actually
// shows and echoes back; a human-readable ID both reads better in that
// button/list and is inherently unambiguous, unlike Name (two pregens
// could share a display name). CharacterJSON is trusted verbatim, same
// level of trust as everything else an operator pastes into this
// panel — Master does not validate its SRD-legality here, only that it
// parses as JSON.
type pregenDTO struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	SchemaVersion string          `json:"schema_version"`
	CharacterJSON json.RawMessage `json:"character_json"`
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
		PriceMultiplier:         settings.PriceMultiplier,
		MinLevel:                settings.MinLevel,
		MaxLevel:                settings.MaxLevel,
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
	if dto.PriceMultiplier < 0 {
		s.writeErrorMsg(w, http.StatusBadRequest, "price_multiplier must not be negative")
		return
	}
	if dto.MinLevel < 0 || dto.MaxLevel < 0 {
		s.writeErrorMsg(w, http.StatusBadRequest, "min_level/max_level must not be negative")
		return
	}
	if dto.MinLevel > 0 && dto.MaxLevel > 0 && dto.MinLevel > dto.MaxLevel {
		s.writeErrorMsg(w, http.StatusBadRequest, "min_level must not exceed max_level")
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
	current.PriceMultiplier = dto.PriceMultiplier
	current.MinLevel = dto.MinLevel
	current.MaxLevel = dto.MaxLevel
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

func (s *Server) handleGetCampaignPack(w http.ResponseWriter, r *http.Request) {
	if s.campaignPack == nil {
		s.writeJSON(w, http.StatusOK, campaignPackDTO{})
		return
	}
	pack, ok, err := s.campaignPack.GetCampaignPack(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	if !ok {
		s.writeJSON(w, http.StatusOK, campaignPackDTO{})
		return
	}
	s.writeJSON(w, http.StatusOK, campaignPackDTO{PackDir: pack.PackDir, PackID: pack.PackID})
}

func (s *Server) handlePutCampaignPack(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	if s.campaignPack == nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign packs are not configured on this Master")
		return
	}
	var dto campaignPackDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if dto.PackDir == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "pack_dir is required")
		return
	}

	// Real validation, not a trusted path string: a directory that
	// doesn't actually parse as a campaign pack is rejected outright,
	// the same "gates over prompting" reasoning CLAUDE.md applies to
	// every other mechanical-consequence action in this codebase —
	// binding a bad directory would silently break every location DM
	// tool the next time the DM tries to use one, not fail loudly here
	// where a host can actually see and fix it.
	pack, err := campaignpack.LoadPack(dto.PackDir)
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "pack_dir does not parse as a valid campaign pack: "+err.Error())
		return
	}

	if err := s.campaignPack.SaveCampaignPack(r.Context(), id, dto.PackDir, pack.ID); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, campaignPackDTO{PackDir: dto.PackDir, PackID: pack.ID})
}

func (s *Server) handleListPregens(w http.ResponseWriter, r *http.Request) {
	if s.pregens == nil {
		s.writeJSON(w, http.StatusOK, []pregenDTO{})
		return
	}
	pregens, err := s.pregens.ListPregens(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	dtos := make([]pregenDTO, len(pregens))
	for i, p := range pregens {
		dtos[i] = pregenDTO{ID: p.ID, Name: p.Name, Description: p.Description, SchemaVersion: p.SchemaVersion, CharacterJSON: p.CharacterData}
	}
	s.writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handlePutPregen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "campaign id is required")
		return
	}
	if s.pregens == nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "pregens are not configured on this Master")
		return
	}
	var dto pregenDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if dto.ID == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "id is required")
		return
	}
	if dto.Name == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(dto.CharacterJSON) == 0 || !json.Valid(dto.CharacterJSON) {
		s.writeErrorMsg(w, http.StatusBadRequest, "character_json must be non-empty, valid JSON")
		return
	}

	if err := s.pregens.SavePregen(r.Context(), store.Pregen{
		ID:            dto.ID,
		CampaignID:    id,
		Name:          dto.Name,
		Description:   dto.Description,
		SchemaVersion: dto.SchemaVersion,
		CharacterData: dto.CharacterJSON,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleDeletePregen(w http.ResponseWriter, r *http.Request) {
	if s.pregens == nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "pregens are not configured on this Master")
		return
	}
	if err := s.pregens.DeletePregen(r.Context(), r.PathValue("pregenId")); err != nil {
		s.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// characterDTO is the Character Review tab's wire shape — every
// character store.CharacterStore.ListCharacters returns for a campaign,
// player-owned and NPC alike (the admin-web UI is responsible for
// filtering/grouping by Status; this handler doesn't second-guess that).
type characterDTO struct {
	ID            string          `json:"id"`
	OwnerID       string          `json:"owner_id"`
	Status        string          `json:"status"`
	SchemaVersion string          `json:"schema_version"`
	CharacterJSON json.RawMessage `json:"character_json"`
	CreatedAt     time.Time       `json:"created_at"`
}

// characterReviewRequestDTO is handleReviewCharacter's request body — a
// Host's approve/reject decision (design doc §9.4's character-import
// veto).
type characterReviewRequestDTO struct {
	// Status must be "approved" or "rejected" — never "pending_review",
	// since submitting a review is what concludes one, not what resets a
	// character back into the queue.
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (s *Server) handleListCharacters(w http.ResponseWriter, r *http.Request) {
	if s.characters == nil {
		s.writeJSON(w, http.StatusOK, []characterDTO{})
		return
	}
	characters, err := s.characters.ListCharacters(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	dtos := make([]characterDTO, len(characters))
	for i, c := range characters {
		dtos[i] = characterDTO{
			ID: c.ID, OwnerID: c.OwnerID, Status: string(c.Status),
			SchemaVersion: c.SchemaVersion, CharacterJSON: c.CharacterData, CreatedAt: c.CreatedAt,
		}
	}
	s.writeJSON(w, http.StatusOK, dtos)
}

// handleReviewCharacter is the Host's veto/approval endpoint (design doc
// §9.4): sets characterId's Status to the Host's decision and, when a
// live hub is configured, pushes character.review_result straight to
// that character's own owner (sendToSender-equivalent — see hub's own
// doc comment on *Server) so a connected player finds out immediately,
// not just on their next reconnect. A Host decision always overrides
// whatever the automatic post-upload review pass (internal/server/
// character_review.go) already decided — the Host is this system's
// final authority, nothing here checks or defers to that prior status.
func (s *Server) handleReviewCharacter(w http.ResponseWriter, r *http.Request) {
	if s.characters == nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "characters are not configured on this Master")
		return
	}
	campaignID := r.PathValue("id")
	characterID := r.PathValue("characterId")

	var dto characterReviewRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	status := store.CharacterStatus(dto.Status)
	if !status.IsValid() || status == store.CharacterStatusPendingReview {
		s.writeErrorMsg(w, http.StatusBadRequest, "status must be \"approved\" or \"rejected\"")
		return
	}

	character, err := s.characters.GetCharacter(r.Context(), characterID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if character.CampaignID != campaignID {
		s.writeErrorMsg(w, http.StatusNotFound, "character does not belong to this campaign")
		return
	}

	character.Status = status
	character.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(r.Context(), character); err != nil {
		s.writeError(w, err)
		return
	}

	if s.hub != nil && character.OwnerID != "" {
		msg, err := newReviewResultMessage(campaignID, protocol.CharacterReviewResultPayload{
			CharacterID: character.ID,
			Status:      string(status),
			Reason:      dto.Reason,
		})
		if err != nil {
			s.logger.Warn("failed to build character.review_result", "error", err, "character_id", character.ID)
		} else if payload, err := json.Marshal(msg); err != nil {
			s.logger.Warn("failed to marshal character.review_result", "error", err, "character_id", character.ID)
		} else {
			s.hub.SendToSender(campaignID, character.OwnerID, payload)
		}
	}

	s.writeJSON(w, http.StatusOK, characterDTO{
		ID: character.ID, OwnerID: character.OwnerID, Status: string(character.Status),
		SchemaVersion: character.SchemaVersion, CharacterJSON: character.CharacterData, CreatedAt: character.CreatedAt,
	})
}

// newReviewResultMessage builds a character.review_result Message —
// package server's own newMessage helper isn't exported, and this is
// the only message package admin ever originates, so a small
// self-contained builder here is simpler than exporting a shared one
// for a single caller.
func newReviewResultMessage(campaignID string, payload protocol.CharacterReviewResultPayload) (protocol.Message[protocol.CharacterReviewResultPayload], error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return protocol.Message[protocol.CharacterReviewResultPayload]{}, errors.New("admin: generating message id failed")
	}
	return protocol.Message[protocol.CharacterReviewResultPayload]{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       hex.EncodeToString(b[:]),
			Timestamp:       time.Now().UTC(),
			SenderID:        "master",
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterReviewResult,
		},
		Payload: payload,
	}, nil
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
