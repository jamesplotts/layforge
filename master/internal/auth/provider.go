// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package auth defines Master's join-authorization contract and its
// implementations: a per-campaign room password (RoomPasswordProvider)
// and Discord OAuth (DiscordOAuthProvider, design doc §6.6's reference
// provider, chosen there because voice chat is already Discord-centric).
// This is the seam design doc §6.6 describes: "Designed as a provider
// interface generally, so a bare room-code scheme or another OAuth
// provider could substitute." The providers compose — DiscordOAuthProvider
// can wrap a RoomPasswordProvider so a campaign requires both a login and
// a room password.
package auth

import "context"

// Identity is the verified player identity a Provider asserts for an
// authorized join. Its zero value means "no identity" — the join was
// authorized (a correct room password, or an open campaign) but the
// provider knows nothing about who the player is. A non-empty AccountID
// is what design doc §9.4's account-keyed character ownership keys on;
// Master treats such a connection as authenticated and stops trusting
// the client-declared sender_id for ownership decisions.
type Identity struct {
	// AccountID is Master's stable account identifier (store.Account.ID,
	// e.g. "discord:8035..."), or "" when the provider has no identity to
	// assert.
	AccountID string
	// DisplayName is the player's handle for display to other players.
	// Empty when AccountID is.
	DisplayName string
	// AvatarURL is a link to the player's avatar image, or "".
	AvatarURL string
}

// Authenticated reports whether i carries a real verified identity (a
// non-empty AccountID), as opposed to the zero Identity an open or
// room-password-only join produces.
func (i Identity) Authenticated() bool { return i.AccountID != "" }

// Result is the outcome of a Provider.Authorize call.
type Result struct {
	// OK reports whether the join is authorized.
	OK bool
	// Reason is a human-readable explanation when OK is false (wrong
	// password, expired login, declined consent). Empty when OK is true.
	// This is not an error — see Provider.Authorize.
	Reason string
	// Identity is the verified identity for the join. Zero unless a
	// provider in the chain asserted one.
	Identity Identity
}

// Provider authorizes a system.connect attempt to join a campaign.
// Master calls it, if configured, before admitting a handshake — see
// package server's use of it.
type Provider interface {
	// Authorize reports whether authToken authorizes joining campaignID,
	// and — when a provider can — who the player is.
	//
	// A non-nil error is reserved for the provider being unable to even
	// perform the check (a database read failing, an OAuth provider's API
	// being unreachable). An expected "not authorized" outcome (wrong
	// password, expired token, declined OAuth consent) is Result{OK:
	// false, Reason: ...} with a nil error.
	Authorize(ctx context.Context, campaignID, authToken string) (Result, error)
}
