// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package auth defines Master's join-authorization contract and its
// first implementation (a per-campaign room password). This is the seam
// design doc §6.6 describes: "Designed as a provider interface
// generally, so a bare room-code scheme or another OAuth provider could
// substitute." RoomPasswordProvider is that room-code scheme; a future
// Discord-OAuth-backed Provider (§6.6's reference provider, chosen
// there specifically because voice chat is already Discord-centric) is
// meant to satisfy this exact same interface, not require reshaping it.
package auth

import "context"

// Provider authorizes a system.connect attempt to join a campaign.
// Master calls it, if configured, before admitting a handshake — see
// package server's use of it.
type Provider interface {
	// Authorize reports whether authToken authorizes joining campaignID.
	//
	// ok is false with a human-readable reason for an expected "not
	// authorized" outcome (wrong password, expired token, declined
	// OAuth consent, ...) — that is not err. err is reserved for the
	// provider being unable to even perform the check (e.g. a future
	// OAuth-backed provider whose call to Discord's API fails).
	Authorize(ctx context.Context, campaignID, authToken string) (ok bool, reason string, err error)
}
