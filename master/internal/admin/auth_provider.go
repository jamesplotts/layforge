// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"context"
	"crypto/subtle"

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// AuthProvider implements auth.Provider by reading a campaign's room
// password live from store.AdminSettingsStore (design doc §3.3, §6.6) —
// same live-read, no-cache reasoning as PolicyProvider. A campaign with
// no admin-saved row, or one saved with an empty password, falls back to
// Fallback (typically the -room-passwords-loaded
// auth.RoomPasswordProvider main.go already constructs, or nil), so a
// campaign never touched via the admin panel keeps behaving exactly as
// it did before this package existed.
type AuthProvider struct {
	store    store.AdminSettingsStore
	Fallback auth.Provider
}

var _ auth.Provider = (*AuthProvider)(nil)

// NewAuthProvider creates an AuthProvider backed by s, falling back to
// fallback (which may be nil, meaning every campaign is open) for a
// campaign with no admin-saved room password.
func NewAuthProvider(s store.AdminSettingsStore, fallback auth.Provider) *AuthProvider {
	return &AuthProvider{store: s, Fallback: fallback}
}

// Authorize implements auth.Provider. It asserts no Identity of its own —
// a room password says nothing about who the player is — but passes
// through whatever Identity the Fallback returns (a DiscordOAuthProvider
// wrapping this one supplies its identity before delegating here).
func (p *AuthProvider) Authorize(ctx context.Context, campaignID, authToken string) (auth.Result, error) {
	settings, ok, err := p.store.GetCampaignSettings(ctx, campaignID)
	if err != nil {
		return auth.Result{}, err
	}
	if !ok || settings.RoomPassword == "" {
		if p.Fallback != nil {
			return p.Fallback.Authorize(ctx, campaignID, authToken)
		}
		return auth.Result{OK: true}, nil
	}
	// Constant-time comparison, same reasoning as
	// auth.RoomPasswordProvider.Authorize: campaign_id is often
	// guessable/advertised, so the password is the only real barrier —
	// worth not leaking timing information about it.
	if subtle.ConstantTimeCompare([]byte(authToken), []byte(settings.RoomPassword)) != 1 {
		return auth.Result{OK: false, Reason: "incorrect password"}, nil
	}
	return auth.Result{OK: true}, nil
}
