// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/terms"
)

// System-tab setting keys, as stored in store.AdminSettingsStore's
// system_settings table (design doc §3.3). Exported so main.go can build
// the seed map it passes to New from the same flag values it already
// parses, without either side hardcoding the key strings twice.
const (
	SystemKeyAddr             = "addr"
	SystemKeyLLMURL           = "llm_url"
	SystemKeyLLMModel         = "llm_model"
	SystemKeyLLMProvider      = "llm_provider"
	SystemKeyLLMAPIKey        = "llm_api_key"
	SystemKeySystemEngineAddr = "system_engine_addr"
	SystemKeyComfyUIURL       = "comfyui_url"
	SystemKeyComfyUIWorkflow  = "comfyui_workflow_path"
	// SystemKeyTermsAcceptedVersion/SystemKeyTermsAcceptedAt record the
	// Host's own acceptance of internal/terms.OperatorText (see
	// EffectiveSystemSettings' callers and the /api/terms endpoints
	// below) — not part of systemKeys/systemSettingsDTO, since these
	// aren't a System-tab setting an operator edits directly, only ever
	// written by handleAcceptTerms or main.go's -accept-terms-version
	// scripted-acceptance path.
	SystemKeyTermsAcceptedVersion = "terms_accepted_version"
	SystemKeyTermsAcceptedAt      = "terms_accepted_at"
)

// systemKeys is every recognized System-tab key, in the fixed order the
// admin UI displays them.
var systemKeys = []string{
	SystemKeyAddr,
	SystemKeyLLMURL,
	SystemKeyLLMModel,
	SystemKeyLLMProvider,
	SystemKeyLLMAPIKey,
	SystemKeySystemEngineAddr,
	SystemKeyComfyUIURL,
	SystemKeyComfyUIWorkflow,
}

// EffectiveSystemSettings merges seed (typically the CLI flag values
// Master actually booted with) with stored (whatever the admin panel has
// saved via SaveSystemSettings) — a key present in stored wins, a key
// absent from stored falls back to seed. Exported so main.go's own boot
// sequence can apply the same "a saved System-tab setting overrides the
// launch flags" resolution design doc §3.3 describes, not just
// handleGetSystem's display of it.
func EffectiveSystemSettings(seed, stored map[string]string) map[string]string {
	effective := make(map[string]string, len(systemKeys))
	for _, key := range systemKeys {
		effective[key] = seed[key]
	}
	for key, value := range stored {
		effective[key] = value
	}
	return effective
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
	// llmProvider/llmModel back the "Generate a campaign pack with AI"
	// flow (handleGenerateCampaignPack) — the same provider/model
	// narration and the DM tool-use loop already use (main.go passes
	// the identical values), not a separately configured one. nil
	// llmProvider means that endpoint rejects with a real "not
	// configured" error, the same nil-disables-the-feature pattern
	// every other optional Server dependency already uses.
	llmProvider llm.Provider
	llmModel    string
	// campaignPacksDir is the root a generated pack's own directory is
	// created under (campaignpack.WriteAndValidate) — unrelated to, and
	// not required to coincide with, wherever a Host keeps hand-authored
	// packs; binding still accepts any path via the existing
	// PUT /api/campaigns/{id}/pack, unaffected by this field.
	campaignPacksDir string
	// registryURL is the same layforge.org-style registry base URL
	// main.go's heartbeat loop uses (the -registry-url flag), reused here
	// only to derive the default download source for the "Install the
	// campaign pack library" flow (handleInstallCampaignPackLibrary) —
	// empty falls back to https://layforge.org, and the operator can
	// always override the URL in the panel regardless.
	registryURL string
	// httpClient fetches the pack-library archive for
	// handleInstallCampaignPackLibrary. A dedicated client with its own
	// timeout, not http.DefaultClient, so a slow or hung download can't
	// pin the request forever.
	httpClient *http.Client
}

// New creates a Server. addr is this admin listener's own bind address
// (e.g. "127.0.0.1:8090"), used only to compute the same-origin check —
// New does not itself listen on it. systemSeed should contain every key
// in systemKeys, seeded from the CLI flag values Master actually started
// with; a key GetSystemSettings has never stored an override for falls
// back to this map (see handleGetSystem). restartRequested is the
// send-only end of a channel main.go's run() selects on.
func New(logger *slog.Logger, s store.AdminSettingsStore, campaignPack store.CampaignPackStore, pregens store.PregenStore, characters store.CharacterStore, webDir, addr string, systemSeed map[string]string, restartRequested chan<- struct{}, llmProvider llm.Provider, llmModel, campaignPacksDir, registryURL string, hub *session.Hub) *Server {
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
		llmProvider:      llmProvider,
		llmModel:         llmModel,
		campaignPacksDir: campaignPacksDir,
		registryURL:      registryURL,
		httpClient:       &http.Client{Timeout: 60 * time.Second},
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
	mux.HandleFunc("POST /api/campaign-packs/generate", s.requireSameOrigin(s.handleGenerateCampaignPack))
	mux.HandleFunc("POST /api/campaign-packs/save", s.requireSameOrigin(s.handleSaveCampaignPack))
	mux.HandleFunc("POST /api/campaign-packs/install-library", s.requireSameOrigin(s.handleInstallCampaignPackLibrary))
	mux.HandleFunc("GET /api/campaigns/{id}/pregens", s.handleListPregens)
	mux.HandleFunc("PUT /api/campaigns/{id}/pregens", s.requireSameOrigin(s.handlePutPregen))
	mux.HandleFunc("DELETE /api/campaigns/{id}/pregens/{pregenId}", s.requireSameOrigin(s.handleDeletePregen))
	mux.HandleFunc("GET /api/campaigns/{id}/characters", s.handleListCharacters)
	mux.HandleFunc("PUT /api/campaigns/{id}/characters/{characterId}/review", s.requireSameOrigin(s.handleReviewCharacter))
	mux.HandleFunc("GET /api/campaigns/{id}/players", s.handleListConnectedPlayers)
	mux.HandleFunc("POST /api/campaigns/{id}/players/{senderId}/kick", s.requireSameOrigin(s.handleKickPlayer))
	mux.HandleFunc("GET /api/system", s.handleGetSystem)
	mux.HandleFunc("PUT /api/system", s.requireSameOrigin(s.handlePutSystem))
	mux.HandleFunc("POST /api/system/restart", s.requireSameOrigin(s.handleRestart))
	mux.HandleFunc("POST /api/system/test-llm", s.requireSameOrigin(s.handleTestLLM))
	mux.HandleFunc("GET /api/terms", s.handleGetTerms)
	mux.HandleFunc("POST /api/terms/accept", s.requireSameOrigin(s.handleAcceptTerms))
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
	PvPPolicy               string `json:"pvp_policy"`
	MaturityTierPrompt      string `json:"maturity_tier_prompt"`
	ImageMaturityTierPrompt string `json:"image_maturity_tier_prompt"`
	// PriceMultiplier mirrors store.CampaignSettings.PriceMultiplier — 0
	// means "not set", resolved to 1.0 by
	// policy.CampaignPolicy.EffectivePriceMultiplier.
	PriceMultiplier float64 `json:"price_multiplier"`
	// MinLevel/MaxLevel mirror store.CampaignSettings's own fields of the
	// same name (design doc §9.4's character-import review flow) — 0 in
	// either means "no bound in that direction".
	MinLevel int `json:"min_level"`
	MaxLevel int `json:"max_level"`
	// MaxPlayers/RegistryListed/JoinAddress mirror store.CampaignSettings's
	// own fields of the same name — the optional public listing at
	// layforge.org (see internal/registry). RegistryListed is the real
	// opt-in gate; false by default.
	MaxPlayers     int    `json:"max_players"`
	RegistryListed bool   `json:"registry_listed"`
	JoinAddress    string `json:"join_address"`
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

// generatedFileDTO is one file in the AI campaign-pack generation
// flow's wire shape — the same fields as campaignpack.GeneratedFile,
// kept as a separate type so this package's own JSON tags don't leak
// into campaignpack's Go-facing struct.
type generatedFileDTO struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// generateCampaignPackRequestDTO is POST /api/campaign-packs/generate's
// request body. Slug is optional — a description-derived one is used
// when omitted (see handleGenerateCampaignPack). Mode selects which of
// campaignpack's three Generate* functions gets called: "" (default)
// generates a brand-new pack from Description/MinLevel/MaxLevel/Slug;
// "chapter" generates one more chapter into the already-saved pack at
// PackDir (ChapterID required); "side_quest" generates a short,
// self-contained side adventure into the pack at PackDir (Description
// and MaxPlayers apply, MinLevel/MaxLevel/Slug are ignored).
type generateCampaignPackRequestDTO struct {
	Description string `json:"description"`
	MinLevel    int    `json:"min_level"`
	MaxLevel    int    `json:"max_level"`
	Slug        string `json:"slug"`
	Mode        string `json:"mode"`
	PackDir     string `json:"pack_dir"`
	ChapterID   string `json:"chapter_id"`
	MaxPlayers  int    `json:"max_players"`
}

type generateCampaignPackResponseDTO struct {
	Slug  string             `json:"slug"`
	Files []generatedFileDTO `json:"files"`
	// ValidationError is set when the generated files don't yet parse
	// as a valid pack (e.g. a single file's YAML front matter has a
	// syntax mistake — confirmed live against a real local model as a
	// real, non-rare occurrence) — files are still returned for the
	// Host to review and fix in place, rather than discarding an
	// otherwise-good multi-minute generation over one fixable file.
	// Save re-validates for real regardless of this field.
	ValidationError string `json:"validation_error,omitempty"`
}

// saveCampaignPackRequestDTO is POST /api/campaign-packs/save's request
// body — the Host's (possibly hand-edited, after reviewing a generate
// response) final file set. Mode mirrors generateCampaignPackRequestDTO's
// own field: "" (default) writes Files as a brand-new pack under Slug;
// "chapter"/"side_quest" merges Files into the already-saved pack at
// PackDir instead (Slug is ignored).
type saveCampaignPackRequestDTO struct {
	Slug    string             `json:"slug"`
	Files   []generatedFileDTO `json:"files"`
	Mode    string             `json:"mode"`
	PackDir string             `json:"pack_dir"`
}

type saveCampaignPackResponseDTO struct {
	// PackDir is handed straight to the existing, unmodified
	// PUT /api/campaigns/{id}/pack to actually bind it — no new bind
	// logic exists anywhere for a generated pack.
	PackDir string `json:"pack_dir"`
}

// handleGenerateCampaignPack calls the configured LLM once and
// validates the result in a throwaway temp directory before ever
// returning it — catching a malformed generation immediately rather
// than letting the Host review something that would fail to bind or
// merge anyway. Nothing is written to the real campaign pack here; see
// handleSaveCampaignPack for that. dto.Mode selects which of the three
// underlying flows runs: handleGenerateFullPack ("") creates a new
// pack; handleGenerateSupplementalPack ("chapter"/"side_quest")
// generates more content into an already-saved one.
func (s *Server) handleGenerateCampaignPack(w http.ResponseWriter, r *http.Request) {
	if s.llmProvider == nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "no LLM provider is configured on this Master — set one up on the System tab first")
		return
	}
	var dto generateCampaignPackRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	switch dto.Mode {
	case "":
		s.handleGenerateFullPack(w, r, dto)
	case "chapter", "side_quest":
		s.handleGenerateSupplementalPack(w, r, dto)
	default:
		s.writeErrorMsg(w, http.StatusBadRequest, `invalid mode: want "", "chapter", or "side_quest"`)
	}
}

func (s *Server) handleGenerateFullPack(w http.ResponseWriter, r *http.Request, dto generateCampaignPackRequestDTO) {
	if strings.TrimSpace(dto.Description) == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "description is required")
		return
	}

	slugSource := dto.Slug
	if slugSource == "" {
		slugSource = dto.Description
	}
	slug, err := campaignpack.SanitizeSlug(slugSource)
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "could not derive a valid slug: "+err.Error())
		return
	}

	files, err := campaignpack.Generate(r.Context(), s.llmProvider, s.llmModel, campaignpack.GenerateRequest{
		Description: dto.Description,
		MinLevel:    dto.MinLevel,
		MaxLevel:    dto.MaxLevel,
	})
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadGateway, "generation failed: "+err.Error())
		return
	}

	// Pre-validate so the Host finds out immediately whether this
	// generation is bindable as-is — but a validation failure still
	// returns the files for review/editing rather than discarding an
	// otherwise-good multi-minute generation over one fixable file
	// (confirmed live: a single file's YAML front matter having a
	// syntax mistake is a real, non-rare model quirk, not a hopeless
	// generation). Save re-validates for real regardless.
	var validationError string
	tempDir, err := os.MkdirTemp("", "layforge-campaign-pack-preview-*")
	if err != nil {
		s.writeError(w, err)
		return
	}
	if _, err := campaignpack.WriteAndValidate(tempDir, "preview", files); err != nil {
		validationError = err.Error()
	}
	_ = os.RemoveAll(tempDir)

	s.writeJSON(w, http.StatusOK, generateCampaignPackResponseDTO{Slug: slug, Files: toFileDTOs(files), ValidationError: validationError})
}

// handleGenerateSupplementalPack handles Mode == "chapter" or
// "side_quest": generating more content into an already-saved pack
// (campaignpack.GenerateChapter/GenerateSideQuest), pre-validated
// against a scratch copy of the existing pack directory so a Host finds
// out immediately whether the result merges cleanly — the same
// "review before committing" behavior handleGenerateFullPack gives a
// brand-new pack, without ever touching the real pack directory until
// handleSaveCampaignPack is called.
func (s *Server) handleGenerateSupplementalPack(w http.ResponseWriter, r *http.Request, dto generateCampaignPackRequestDTO) {
	if dto.PackDir == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "pack_dir is required")
		return
	}

	var files []campaignpack.GeneratedFile
	var err error
	switch dto.Mode {
	case "chapter":
		if dto.ChapterID == "" {
			s.writeErrorMsg(w, http.StatusBadRequest, "chapter_id is required")
			return
		}
		files, err = campaignpack.GenerateChapter(r.Context(), s.llmProvider, s.llmModel, campaignpack.GenerateChapterRequest{
			ExistingPackDir: dto.PackDir,
			ChapterID:       dto.ChapterID,
		})
	case "side_quest":
		if strings.TrimSpace(dto.Description) == "" {
			s.writeErrorMsg(w, http.StatusBadRequest, "description is required")
			return
		}
		files, err = campaignpack.GenerateSideQuest(r.Context(), s.llmProvider, s.llmModel, campaignpack.GenerateSideQuestRequest{
			ExistingPackDir: dto.PackDir,
			Description:     dto.Description,
			MaxPlayers:      dto.MaxPlayers,
		})
	}
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadGateway, "generation failed: "+err.Error())
		return
	}

	var validationError string
	tempDir, err := os.MkdirTemp("", "layforge-campaign-pack-preview-*")
	if err != nil {
		s.writeError(w, err)
		return
	}
	if copyErr := os.CopyFS(tempDir, os.DirFS(dto.PackDir)); copyErr != nil {
		_ = os.RemoveAll(tempDir)
		s.writeError(w, copyErr)
		return
	}
	if addErr := campaignpack.AddFilesAndValidate(tempDir, files); addErr != nil {
		validationError = addErr.Error()
	}
	_ = os.RemoveAll(tempDir)

	s.writeJSON(w, http.StatusOK, generateCampaignPackResponseDTO{Files: toFileDTOs(files), ValidationError: validationError})
}

func toFileDTOs(files []campaignpack.GeneratedFile) []generatedFileDTO {
	out := make([]generatedFileDTO, len(files))
	for i, f := range files {
		out[i] = generatedFileDTO{Path: f.Path, Content: f.Content}
	}
	return out
}

// handleSaveCampaignPack persists the Host's (possibly hand-edited)
// reviewed file set for real. dto.Mode == "" (default) writes Files as
// a brand-new pack under s.campaignPacksDir/Slug via
// campaignpack.WriteAndValidate; "chapter"/"side_quest" instead merges
// Files into the already-saved pack at dto.PackDir via
// campaignpack.AddFilesAndValidate — see each for the sandboxing and
// real-parser validation this depends on. The returned pack_dir is
// meant to be handed straight to the existing PUT
// /api/campaigns/{id}/pack by the admin-web UI, not bound here.
func (s *Server) handleSaveCampaignPack(w http.ResponseWriter, r *http.Request) {
	var dto saveCampaignPackRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(dto.Files) == 0 {
		s.writeErrorMsg(w, http.StatusBadRequest, "files is required and must not be empty")
		return
	}
	files := make([]campaignpack.GeneratedFile, len(dto.Files))
	for i, f := range dto.Files {
		files[i] = campaignpack.GeneratedFile{Path: f.Path, Content: f.Content}
	}

	switch dto.Mode {
	case "":
		if s.campaignPacksDir == "" {
			s.writeErrorMsg(w, http.StatusBadRequest, "no campaign-packs directory is configured on this Master")
			return
		}
		if dto.Slug == "" {
			s.writeErrorMsg(w, http.StatusBadRequest, "slug is required")
			return
		}
		dir, err := campaignpack.WriteAndValidate(s.campaignPacksDir, dto.Slug, files)
		if err != nil {
			s.writeErrorMsg(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, saveCampaignPackResponseDTO{PackDir: dir})
	case "chapter", "side_quest":
		if dto.PackDir == "" {
			s.writeErrorMsg(w, http.StatusBadRequest, "pack_dir is required")
			return
		}
		if err := campaignpack.AddFilesAndValidate(dto.PackDir, files); err != nil {
			s.writeErrorMsg(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, saveCampaignPackResponseDTO{PackDir: dto.PackDir})
	default:
		s.writeErrorMsg(w, http.StatusBadRequest, `invalid mode: want "", "chapter", or "side_quest"`)
	}
}

// defaultPackLibraryURL is the download source
// handleInstallCampaignPackLibrary falls back to when neither the request
// body nor -registry-url names one — the project's own public registry.
const defaultPackLibraryURL = "https://layforge.org"

// packLibraryArchivePath is appended to a registry base URL to reach the
// hosted pack-library zip (registry/web/downloads/).
const packLibraryArchivePath = "/downloads/campaign-pack-library.zip"

// maxPackLibraryDownloadBytes caps the archive this endpoint will pull
// over HTTP, matching campaignpack's own internal archive-size limit so a
// download that would be rejected on unpack is rejected before it's even
// fully read.
const maxPackLibraryDownloadBytes = 32 << 20

// installLibraryRequestDTO is POST /api/campaign-packs/install-library's
// body. URL is optional — empty derives the source from -registry-url
// (or defaultPackLibraryURL). Overwrite replaces packs whose slug
// directory already exists; without it those are reported skipped.
type installLibraryRequestDTO struct {
	URL       string `json:"url"`
	Overwrite bool   `json:"overwrite"`
}

// installLibraryResultDTO is one pack's outcome, mirroring
// campaignpack.InstallResult. Status is one of "installed",
// "skipped_exists", "failed"; detail is set only for "failed".
// Campaign is the campaign_id this pack was made selectable under (its
// own campaign.md id); CampaignAdded is true when that campaign was
// created by this install rather than already existing.
type installLibraryResultDTO struct {
	Slug          string `json:"slug"`
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
	Campaign      string `json:"campaign,omitempty"`
	CampaignAdded bool   `json:"campaign_added,omitempty"`
}

type installLibraryResponseDTO struct {
	// Source is the URL the archive was actually fetched from, echoed
	// back so the panel can show which registry answered.
	Source  string                    `json:"source"`
	Results []installLibraryResultDTO `json:"results"`
	// CampaignsAdded counts the pregenerated campaigns this install
	// newly made selectable — the panel uses it to decide whether to
	// refresh the campaign dropdown.
	CampaignsAdded int `json:"campaigns_added"`
}

// handleInstallCampaignPackLibrary downloads a pack-library archive and
// unpacks it into s.campaignPacksDir via campaignpack.InstallLibrary,
// which re-validates every pack with the same LoadPack gate a
// hand-authored or generated pack goes through and enforces the archive
// path/size limits (zip-slip, zip-bomb) in code — this handler never
// trusts the archive's contents. Each pack's outcome is reported
// independently; a single unparseable pack does not fail the request.
//
// Each pack that lands on disk is then made directly selectable: a
// campaign is created under the pack's own campaign.md id (display name
// = its title) and the pack bound to it, so the Host can pick a
// pregenerated adventure straight from the panel's campaign dropdown
// without a separate create-then-bind step. This is skipped for a
// campaign id that already has a pack bound — a re-run, or a collision
// with the Host's own campaign, never clobbers an existing binding.
func (s *Server) handleInstallCampaignPackLibrary(w http.ResponseWriter, r *http.Request) {
	if s.campaignPacksDir == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "no campaign-packs directory is configured on this Master")
		return
	}

	var dto installLibraryRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil && !errors.Is(err, io.EOF) {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	source, err := s.resolvePackLibraryURL(dto.URL)
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, err.Error())
		return
	}

	archive, err := s.downloadPackLibrary(r.Context(), source)
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadGateway, err.Error())
		return
	}

	results, err := campaignpack.InstallLibrary(bytes.NewReader(archive), int64(len(archive)), s.campaignPacksDir, campaignpack.InstallOptions{Overwrite: dto.Overwrite})
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, err.Error())
		return
	}

	out := installLibraryResponseDTO{Source: source, Results: make([]installLibraryResultDTO, len(results))}
	for i, res := range results {
		dto := installLibraryResultDTO{Slug: res.Slug, Status: string(res.Status), Detail: res.Detail}
		if res.Status == campaignpack.InstallStatusInstalled || res.Status == campaignpack.InstallStatusSkippedExists {
			campaignID, added := s.bindInstalledPackToCampaign(r.Context(), res.Slug)
			dto.Campaign = campaignID
			dto.CampaignAdded = added
			if added {
				out.CampaignsAdded++
			}
		}
		out.Results[i] = dto
	}
	s.writeJSON(w, http.StatusOK, out)
}

// bindInstalledPackToCampaign creates a campaign for the just-installed
// pack directory slug (campaign_id = the pack's own campaign.md id,
// display name = its title) and binds the pack to it, so it shows up in
// the panel's campaign dropdown ready to play. Returns the campaign id
// and whether it was newly created. A no-op (returns the id, added
// false) when a pack is already bound to that campaign id, or ("", false)
// when campaign-pack storage isn't configured or the pack won't re-load.
func (s *Server) bindInstalledPackToCampaign(ctx context.Context, slug string) (campaignID string, added bool) {
	if s.campaignPack == nil {
		return "", false
	}
	packDir := filepath.Join(s.campaignPacksDir, slug)
	pack, err := campaignpack.LoadPack(packDir)
	if err != nil {
		s.logger.Warn("installed pack did not re-load; not creating a campaign for it", "slug", slug, "error", err)
		return "", false
	}

	if _, bound, err := s.campaignPack.GetCampaignPack(ctx, pack.ID); err != nil {
		s.logger.Warn("could not check existing pack binding", "campaign_id", pack.ID, "error", err)
		return pack.ID, false
	} else if bound {
		return pack.ID, false // already set up — never clobber
	}

	title := pack.Title
	if title == "" {
		title = pack.ID
	}
	if err := s.store.SaveCampaignMeta(ctx, pack.ID, title); err != nil {
		s.logger.Warn("could not name campaign for installed pack", "campaign_id", pack.ID, "error", err)
		return pack.ID, false
	}
	if err := s.campaignPack.SaveCampaignPack(ctx, pack.ID, packDir, pack.ID); err != nil {
		s.logger.Warn("could not bind installed pack to its campaign", "campaign_id", pack.ID, "error", err)
		return pack.ID, false
	}
	return pack.ID, true
}

// resolvePackLibraryURL picks the archive download URL: an explicit,
// absolute http(s) URL from the request body if given, otherwise
// -registry-url (or defaultPackLibraryURL) plus packLibraryArchivePath.
func (s *Server) resolvePackLibraryURL(requested string) (string, error) {
	if requested != "" {
		u, err := url.Parse(requested)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
			return "", errors.New("url must be an absolute http or https URL")
		}
		return requested, nil
	}
	base := s.registryURL
	if base == "" {
		base = defaultPackLibraryURL
	}
	return strings.TrimRight(base, "/") + packLibraryArchivePath, nil
}

// downloadPackLibrary GETs source and returns its body, capped at
// maxPackLibraryDownloadBytes. A non-2xx response, an over-cap body, or a
// transport error is returned as an error for the caller to surface as a
// 502.
func (s *Server) downloadPackLibrary(ctx context.Context, source string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("building download request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downloading %s: server returned %s", source, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackLibraryDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", source, err)
	}
	if len(body) > maxPackLibraryDownloadBytes {
		return nil, fmt.Errorf("archive at %s is larger than the %d-byte limit", source, maxPackLibraryDownloadBytes)
	}
	return body, nil
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
	Addr     string `json:"addr"`
	LLMURL   string `json:"llm_url"`
	LLMModel string `json:"llm_model"`
	// LLMProvider selects which llm.Provider implementation Master
	// constructs (see llm.ProviderKind) — empty behaves as
	// llm.ProviderKindOllama, matching every self-hoster's config from
	// before this field existed. LLMAPIKey is required for every other
	// provider (validated in handlePutSystem/handleRestart) and is never
	// sent to any Slave client — only ever read here, by this same
	// local-only, operator-trusted admin listener (design doc §3.3).
	LLMProvider string `json:"llm_provider"`
	LLMAPIKey   string `json:"llm_api_key"`

	SystemEngineAddr    string `json:"system_engine_addr"`
	ComfyUIURL          string `json:"comfyui_url"`
	ComfyUIWorkflowPath string `json:"comfyui_workflow_path"`
}

func (d systemSettingsDTO) toMap() map[string]string {
	return map[string]string{
		SystemKeyAddr:             d.Addr,
		SystemKeyLLMURL:           d.LLMURL,
		SystemKeyLLMModel:         d.LLMModel,
		SystemKeyLLMProvider:      d.LLMProvider,
		SystemKeyLLMAPIKey:        d.LLMAPIKey,
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
		LLMProvider:         m[SystemKeyLLMProvider],
		LLMAPIKey:           m[SystemKeyLLMAPIKey],
		SystemEngineAddr:    m[SystemKeySystemEngineAddr],
		ComfyUIURL:          m[SystemKeyComfyUIURL],
		ComfyUIWorkflowPath: m[SystemKeyComfyUIWorkflow],
	}
}

// validateSystemSettings rejects a System-tab save that can't possibly
// work, mirroring handlePutCampaignPolicy's own "opt-in requires its
// companion field" validation shape: an unrecognized LLMProvider value,
// or a non-Ollama provider with no API key to authenticate with.
func validateSystemSettings(dto systemSettingsDTO) string {
	if dto.LLMProvider != "" && !llm.ProviderKind(dto.LLMProvider).IsValid() {
		return "llm_provider must be one of: ollama, anthropic, openai, openrouter, zai"
	}
	kind := llm.ProviderKind(dto.LLMProvider)
	if kind != "" && kind != llm.ProviderKindOllama && dto.LLMAPIKey == "" {
		return "llm_api_key is required for every llm_provider except ollama"
	}
	return ""
}

// testLLMRequestDTO is POST /api/system/test-llm's request body — the
// candidate LLM settings currently sitting in the System tab's form,
// not necessarily saved yet. Deliberately its own small type rather
// than reusing systemSettingsDTO: this endpoint only cares about these
// four fields, not the tab's other, unrelated settings.
type testLLMRequestDTO struct {
	LLMProvider string `json:"llm_provider"`
	LLMURL      string `json:"llm_url"`
	LLMModel    string `json:"llm_model"`
	LLMAPIKey   string `json:"llm_api_key"`
}

type testLLMResponseDTO struct {
	OK bool `json:"ok"`
	// Response is the model's actual reply text — shown back to the
	// Host as extra, concrete confidence that a real completion
	// happened, not just that the HTTP call didn't error.
	Response string `json:"response"`
}

// testLLMTimeout bounds handleTestLLM's own call — much shorter than
// llm.DefaultProviderTimeout (a real generation can legitimately run
// for minutes; a connectivity check is meant to be quick, and a Host
// clicking "Test Connection" shouldn't wait minutes to find out a typo
// broke it).
const testLLMTimeout = 30 * time.Second

// handleTestLLM builds a throwaway llm.Provider from the request body
// (not s.llmProvider — that's the already-active, already-saved
// provider main.go constructed at boot; this endpoint exists
// specifically to try out *unsaved* System-tab form values before
// committing to "Save & Restart") and makes one real, minimal
// completion call against it.
func (s *Server) handleTestLLM(w http.ResponseWriter, r *http.Request) {
	var dto testLLMRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	kind := llm.ProviderKind(dto.LLMProvider)
	if kind == "" {
		kind = llm.ProviderKindOllama
	}
	if !kind.IsValid() {
		s.writeErrorMsg(w, http.StatusBadRequest, "llm_provider must be one of: ollama, anthropic, openai, openrouter, zai")
		return
	}
	if kind != llm.ProviderKindOllama && dto.LLMAPIKey == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "llm_api_key is required for every llm_provider except ollama")
		return
	}

	provider, err := llm.NewProvider(llm.ProviderConfig{Kind: kind, BaseURL: dto.LLMURL, APIKey: dto.LLMAPIKey})
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), testLLMTimeout)
	defer cancel()
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:      dto.LLMModel,
		UserPrompt: "Reply with only the single word: OK",
	})
	if err != nil {
		s.writeErrorMsg(w, http.StatusBadGateway, "test failed: "+err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, testLLMResponseDTO{OK: true, Response: resp.Text})
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
		MaturityTierPrompt:      settings.MaturityTierPrompt,
		ImageMaturityTierPrompt: settings.ImageMaturityTierPrompt,
		PriceMultiplier:         settings.PriceMultiplier,
		MinLevel:                settings.MinLevel,
		MaxLevel:                settings.MaxLevel,
		MaxPlayers:              settings.MaxPlayers,
		RegistryListed:          settings.RegistryListed,
		JoinAddress:             settings.JoinAddress,
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
		s.writeErrorMsg(w, http.StatusBadRequest, "invalid pvp_policy (want pve_only or pvp_allowed)")
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
	if dto.MaxPlayers < 0 {
		s.writeErrorMsg(w, http.StatusBadRequest, "max_players must not be negative")
		return
	}
	if dto.RegistryListed && dto.JoinAddress == "" {
		s.writeErrorMsg(w, http.StatusBadRequest, "join_address is required to list this campaign publicly")
		return
	}

	current, _, err := s.store.GetCampaignSettings(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	current.PvPPolicy = dto.PvPPolicy
	current.MaturityTierPrompt = dto.MaturityTierPrompt
	current.ImageMaturityTierPrompt = dto.ImageMaturityTierPrompt
	current.PriceMultiplier = dto.PriceMultiplier
	current.MinLevel = dto.MinLevel
	current.MaxLevel = dto.MaxLevel
	current.MaxPlayers = dto.MaxPlayers
	current.RegistryListed = dto.RegistryListed
	current.JoinAddress = dto.JoinAddress
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
		msg, err := newAdminMessage(campaignID, protocol.MessageTypeCharacterReviewResult, protocol.CharacterReviewResultPayload{
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

// newAdminMessage builds a Message of the given type — package server's
// own newMessage helper isn't exported, and admin package originates
// only a couple of message types directly (character.review_result,
// system.error for a kicked player), so a small self-contained generic
// builder here is simpler than exporting a shared one just for admin's
// two callers.
func newAdminMessage[T any](campaignID string, msgType protocol.MessageType, payload T) (protocol.Message[T], error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return protocol.Message[T]{}, errors.New("admin: generating message id failed")
	}
	return protocol.Message[T]{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       hex.EncodeToString(b[:]),
			Timestamp:       time.Now().UTC(),
			SenderID:        "master",
			CampaignID:      campaignID,
			Type:            msgType,
		},
		Payload: payload,
	}, nil
}

// connectedPlayersDTO is the response body of GET
// /api/campaigns/{id}/players.
type connectedPlayersDTO struct {
	SenderIDs []string `json:"sender_ids"`
}

// handleListConnectedPlayers reports which sender_ids currently have a
// live connection to campaignID (internal/session.Hub's live
// registration state) — distinct from handleListCharacters, which
// reports persisted characters regardless of whether their owner is
// online right now. Used by the admin panel's Characters tab to
// populate the Kick action's target list.
func (s *Server) handleListConnectedPlayers(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	var senderIDs []string
	if s.hub != nil {
		senderIDs = s.hub.ConnectedSenders(campaignID)
	}
	s.writeJSON(w, http.StatusOK, connectedPlayersDTO{SenderIDs: senderIDs})
}

// kickPlayerResultDTO is the response body of POST
// /api/campaigns/{id}/players/{senderId}/kick.
type kickPlayerResultDTO struct {
	Kicked bool `json:"kicked"`
}

// kickNotificationDelay is how long handleKickPlayer waits after
// queuing the kicked player's notification before forcibly closing
// their connection — just enough for the write pump to actually flush
// it first. There is no shared completion signal between SendToSender
// (an async channel send) and Kick (a synchronous close) to wait on
// instead, and this is a rare, explicit, Host-triggered action rather
// than a hot path, so a short fixed delay is the simplest correct fix.
var kickNotificationDelay = 200 * time.Millisecond

// handleKickPlayer forcibly disconnects senderId's live connection(s)
// to campaignID, if any (design doc §3.3's Host-operator capabilities;
// removing pvp_with_consent left "the Host removes a disruptive player"
// as the real lever instead of a per-player consent list). Not a ban:
// nothing here prevents senderId from reconnecting and rejoining —
// there is no account system in this repo to ban against (§9.4). A
// senderId with no live connection is reported as Kicked: false, not
// an error — the Host may simply be clicking a stale row.
func (s *Server) handleKickPlayer(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	senderID := r.PathValue("senderId")

	if s.hub == nil {
		s.writeJSON(w, http.StatusOK, kickPlayerResultDTO{Kicked: false})
		return
	}

	msg, err := newAdminMessage(campaignID, protocol.MessageTypeSystemError, protocol.SystemErrorPayload{
		Code:    "kicked_by_host",
		Message: "You have been removed from this campaign by the host.",
	})
	if err != nil {
		s.logger.Warn("failed to build kick notification", "error", err, "campaign_id", campaignID, "sender_id", senderID)
	} else if payload, err := json.Marshal(msg); err != nil {
		s.logger.Warn("failed to marshal kick notification", "error", err, "campaign_id", campaignID, "sender_id", senderID)
	} else {
		s.hub.SendToSender(campaignID, senderID, payload)
	}

	time.Sleep(kickNotificationDelay)

	s.writeJSON(w, http.StatusOK, kickPlayerResultDTO{Kicked: s.hub.Kick(campaignID, senderID)})
}

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	stored, err := s.store.GetSystemSettings(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	effective := EffectiveSystemSettings(s.systemSeed, stored)
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
	if msg := validateSystemSettings(dto); msg != "" {
		s.writeErrorMsg(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.saveSystemSettings(r.Context(), dto); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

// termsDTO is GET /api/terms' and POST /api/terms/accept's shared wire
// shape. Version/OperatorText are always the current build's
// terms.Version/terms.OperatorText — a self-hoster can't be shown a
// stale text even if their SQLite database is old, since these are
// compiled-in, not stored. Accepted/AcceptedAt reflect whether the
// stored SystemKeyTermsAcceptedVersion actually matches the current
// Version (an old acceptance of a since-changed text does not count).
type termsDTO struct {
	Version      string `json:"version"`
	OperatorText string `json:"operator_text"`
	Accepted     bool   `json:"accepted"`
	AcceptedAt   string `json:"accepted_at,omitempty"`
}

// OperatorTermsAccepted reports whether stored (as returned by
// store.AdminSettingsStore.GetSystemSettings) reflects acceptance of the
// current terms.Version. Exported so both main.go's boot-time gate and
// internal/server's dispatch gate share this one comparison rather than
// each re-deriving it.
func OperatorTermsAccepted(stored map[string]string) bool {
	return stored[SystemKeyTermsAcceptedVersion] == terms.Version
}

func (s *Server) currentTermsDTO(ctx context.Context) (termsDTO, error) {
	stored, err := s.store.GetSystemSettings(ctx)
	if err != nil {
		return termsDTO{}, err
	}
	dto := termsDTO{Version: terms.Version, OperatorText: terms.OperatorText}
	if OperatorTermsAccepted(stored) {
		dto.Accepted = true
		dto.AcceptedAt = stored[SystemKeyTermsAcceptedAt]
	}
	return dto, nil
}

// handleGetTerms reports whether the Host has accepted the current
// terms.Version yet — the admin panel's own first-load check that
// decides whether to show the blocking Agree modal, and the same
// condition internal/server's dispatch gate checks before processing
// any player message (design doc's own "gates over prompting," applied
// to this feature too: the modal is a UI convenience, this endpoint's
// underlying stored state is what's actually enforced).
func (s *Server) handleGetTerms(w http.ResponseWriter, r *http.Request) {
	dto, err := s.currentTermsDTO(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dto)
}

// handleAcceptTerms records the Host's acceptance of the current
// terms.Version — same-origin-gated like every other mutating admin
// endpoint (see requireSameOrigin's own doc comment).
func (s *Server) handleAcceptTerms(w http.ResponseWriter, r *http.Request) {
	err := s.store.SaveSystemSettings(r.Context(), map[string]string{
		SystemKeyTermsAcceptedVersion: terms.Version,
		SystemKeyTermsAcceptedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	dto, err := s.currentTermsDTO(r.Context())
	if err != nil {
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
		if msg := validateSystemSettings(dto); msg != "" {
			s.writeErrorMsg(w, http.StatusBadRequest, msg)
			return
		}
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
