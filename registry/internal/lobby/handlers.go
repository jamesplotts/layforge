// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package lobby

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// maxRequestBodyBytes caps every write endpoint's request body — plenty
// for these fields (see maxAdventureNameLen/maxJoinURLLen/
// maxCampaignIDLen below), and small enough that a client sending an
// oversized body can't use this handler to allocate unbounded memory.
const maxRequestBodyBytes = 16 * 1024

// Field length caps, generous for any real listing but small enough to
// keep the public page readable and bound how much an attacker can pad
// a single listing by (on top of maxRequestBodyBytes, which bounds the
// whole request).
const (
	maxAdventureNameLen = 200
	maxJoinURLLen       = 500
	maxCampaignIDLen    = 200
)

// validateFields rejects a listing whose fields can't possibly be a real
// join-able campaign: empty required fields (create's own additional
// check), anything over the length caps above, or a join_url that
// doesn't even parse as a URL with a scheme. Returns "" when fields is
// acceptable.
func validateFields(f Fields) string {
	if len(f.AdventureName) > maxAdventureNameLen {
		return "adventure_name is too long"
	}
	if len(f.JoinURL) > maxJoinURLLen {
		return "join_url is too long"
	}
	if len(f.CampaignID) > maxCampaignIDLen {
		return "campaign_id is too long"
	}
	if f.JoinURL != "" {
		u, err := url.Parse(f.JoinURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "join_url must be a valid absolute URL"
		}
	}
	return ""
}

// NewHandler returns the HTTP handler for store's public API:
//
//	POST   /api/v1/listings      create a listing, returns {id, token}
//	PUT    /api/v1/listings/{id} heartbeat/update — body carries token
//	DELETE /api/v1/listings/{id} deregister — body carries token
//	GET    /api/v1/listings      public read, no auth, never includes Token
//
// Unlike master/internal/admin's own API (a local-only operator panel
// gated by same-origin checks against drive-by browser requests), this
// is a genuinely public write API any self-hosted Master may call —
// there is no equivalent same-origin concept for a cross-host server-
// to-server registration call, so authorization here is entirely the
// per-listing Token Create hands back, checked by Heartbeat/Remove.
func NewHandler(store *Store, ttl time.Duration) http.Handler {
	h := &handler{store: store, ttl: ttl}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/listings", h.create)
	mux.HandleFunc("PUT /api/v1/listings/{id}", h.heartbeat)
	mux.HandleFunc("DELETE /api/v1/listings/{id}", h.remove)
	mux.HandleFunc("GET /api/v1/listings", h.list)
	return mux
}

type handler struct {
	store *Store
	ttl   time.Duration
}

// listingRequestBody is the wire shape POST/PUT both accept — Token is
// ignored by POST (a new listing has no token yet) and required by PUT.
type listingRequestBody struct {
	Token             string `json:"token"`
	AdventureName     string `json:"adventure_name"`
	MinLevel          int    `json:"min_level"`
	MaxLevel          int    `json:"max_level"`
	PlayersJoined     int    `json:"players_joined"`
	PlayerSlots       int    `json:"player_slots"`
	PasswordProtected bool   `json:"password_protected"`
	JoinURL           string `json:"join_url"`
	CampaignID        string `json:"campaign_id"`
}

func (b listingRequestBody) fields() Fields {
	return Fields{
		AdventureName:     b.AdventureName,
		MinLevel:          b.MinLevel,
		MaxLevel:          b.MaxLevel,
		PlayersJoined:     b.PlayersJoined,
		PlayerSlots:       b.PlayerSlots,
		PasswordProtected: b.PasswordProtected,
		JoinURL:           b.JoinURL,
		CampaignID:        b.CampaignID,
	}
}

// listingResponseBody is the wire shape GET /api/v1/listings returns —
// deliberately its own type, not Listing itself, so Token (unexported
// here via simply never being a field) can never leak even if Listing
// itself later gains a json tag on Token by mistake.
type listingResponseBody struct {
	ID                string `json:"id"`
	AdventureName     string `json:"adventure_name"`
	MinLevel          int    `json:"min_level"`
	MaxLevel          int    `json:"max_level"`
	PlayersJoined     int    `json:"players_joined"`
	PlayerSlots       int    `json:"player_slots"`
	PasswordProtected bool   `json:"password_protected"`
	JoinURL           string `json:"join_url"`
	CampaignID        string `json:"campaign_id"`
}

// decodeBody wraps r.Body in http.MaxBytesReader (every write endpoint's
// shared defense against an oversized request body) and decodes it into
// v, mapping a MaxBytesReader trip to 413 rather than a generic 400.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErrorMsg(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeErrorMsg(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		}
		return false
	}
	return true
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var body listingRequestBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.AdventureName == "" || body.JoinURL == "" || body.CampaignID == "" {
		writeErrorMsg(w, http.StatusBadRequest, "adventure_name, join_url, and campaign_id are required")
		return
	}
	fields := body.fields()
	if msg := validateFields(fields); msg != "" {
		writeErrorMsg(w, http.StatusBadRequest, msg)
		return
	}
	id, token, err := h.store.Create(fields)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]string{"id": id, "token": token})
	case errors.Is(err, ErrStoreFull):
		writeErrorMsg(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorMsg(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body listingRequestBody
	if !decodeBody(w, r, &body) {
		return
	}
	fields := body.fields()
	if msg := validateFields(fields); msg != "" {
		writeErrorMsg(w, http.StatusBadRequest, msg)
		return
	}
	err := h.store.Heartbeat(r.PathValue("id"), body.Token, fields)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, ErrNotFound):
		writeErrorMsg(w, http.StatusNotFound, "unknown listing id — it may have expired; register again")
	case errors.Is(err, ErrTokenMismatch):
		writeErrorMsg(w, http.StatusForbidden, "token does not match this listing")
	default:
		writeErrorMsg(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	err := h.store.Remove(r.PathValue("id"), body.Token)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		writeErrorMsg(w, http.StatusNotFound, "unknown listing id")
	case errors.Is(err, ErrTokenMismatch):
		writeErrorMsg(w, http.StatusForbidden, "token does not match this listing")
	default:
		writeErrorMsg(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	live := h.store.Live(h.ttl)
	out := make([]listingResponseBody, len(live))
	for i, l := range live {
		out[i] = listingResponseBody{
			ID:                l.ID,
			AdventureName:     l.AdventureName,
			MinLevel:          l.MinLevel,
			MaxLevel:          l.MaxLevel,
			PlayersJoined:     l.PlayersJoined,
			PlayerSlots:       l.PlayerSlots,
			PasswordProtected: l.PasswordProtected,
			JoinURL:           l.JoinURL,
			CampaignID:        l.CampaignID,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": out})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErrorMsg(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
