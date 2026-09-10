// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"net/http"
)

// adminSettingsKeyActiveCampaignID / adminSettingsKeyCampaignJoinLocked
// mirror package admin's SystemKeyActiveCampaignID /
// SystemKeyCampaignJoinLocked — duplicated, not imported, for the same
// dependency-direction reason terms.go's adminSettingsKeyTermsAcceptedVersion
// documents (main.go wires admin on top of server's store interfaces,
// never the reverse).
const (
	adminSettingsKeyActiveCampaignID   = "active_campaign_id"
	adminSettingsKeyCampaignJoinLocked = "campaign_join_locked"
)

// sessionGate enforces the Host's "one game running" state (design doc
// §3.3), checked in handleConnection before a join is admitted:
//
//   - No active campaign set → every join is allowed (back-compat: a
//     Master the Host hasn't configured a Session on, and every existing
//     test client, behaves exactly as before).
//   - An active campaign set → only that campaign is joinable.
//   - The active campaign locked ("closed to new players") → only an
//     actingSender that already owns a character in it may (re)connect;
//     newcomers are turned away until the Host unlocks.
//
// ok is false with a player-facing reason for a refused join; err is only
// for a store read failing. A nil adminSettings (test servers without
// one) means no gate at all.
func (s *Server) sessionGate(ctx context.Context, campaignID, actingSender string) (ok bool, reason string, err error) {
	if s.adminSettings == nil {
		return true, "", nil
	}
	settings, err := s.adminSettings.GetSystemSettings(ctx)
	if err != nil {
		return false, "", err
	}
	active := settings[adminSettingsKeyActiveCampaignID]
	if active == "" {
		return true, "", nil
	}
	if campaignID != active {
		return false, "no game by that name is running", nil
	}
	if settings[adminSettingsKeyCampaignJoinLocked] != "true" {
		return true, "", nil
	}

	// Locked: admit only a player who already has a character here.
	if s.characters != nil {
		chars, err := s.characters.ListCharacters(ctx, active)
		if err != nil {
			return false, "", err
		}
		for _, c := range chars {
			if c.OwnerID == actingSender {
				return true, "", nil
			}
		}
	}
	return false, "this game is closed to new players right now", nil
}

// sessionInfoDTO is GET /api/session's public body — what a player's
// client needs to render its join screen. It never carries the room
// password itself, only whether one is required.
type sessionInfoDTO struct {
	CampaignID    string `json:"campaign_id"`
	DisplayName   string `json:"display_name"`
	NeedsPassword bool   `json:"needs_password"`
	JoinLocked    bool   `json:"join_locked"`
}

// SessionInfoHandler serves GET /api/session on the player-facing
// listener (mounted by main.go next to /ws). It reports the active
// campaign, its display name, whether it needs a password, and whether
// it is closed to new players — everything the reworked join screen
// needs, and nothing secret.
func (s *Server) SessionInfoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		out := sessionInfoDTO{}
		if s.adminSettings == nil {
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		settings, err := s.adminSettings.GetSystemSettings(r.Context())
		if err != nil {
			http.Error(w, "could not read session state", http.StatusInternalServerError)
			return
		}
		active := settings[adminSettingsKeyActiveCampaignID]
		if active == "" {
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		out.CampaignID = active
		out.DisplayName = active
		out.JoinLocked = settings[adminSettingsKeyCampaignJoinLocked] == "true"

		if cs, ok, err := s.adminSettings.GetCampaignSettings(r.Context(), active); err == nil && ok {
			out.NeedsPassword = cs.RoomPassword != ""
		}
		// Prefer the campaign's display name; degrade to the raw id rather
		// than failing the endpoint if the summary read errors.
		if summaries, err := s.adminSettings.ListCampaignSummaries(r.Context()); err == nil {
			for _, sum := range summaries {
				if sum.CampaignID == active && sum.DisplayName != "" {
					out.DisplayName = sum.DisplayName
				}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
