// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth

import (
	"context"
	"crypto/subtle"
)

// RoomPasswordProvider authorizes joins via a per-campaign password,
// configured by Master's operator at startup — never by anything a
// player controls. A campaign with no password configured is open to
// anyone, matching the behavior of every campaign_id before this
// provider existed — adding it to a Master's config is opt-in
// protection per campaign, not a change to the default.
type RoomPasswordProvider struct {
	// passwords maps campaign_id to its required password. Read-only
	// after construction — see NewRoomPasswordProvider — so no locking
	// is needed for concurrent Authorize calls.
	passwords map[string]string
}

var _ Provider = (*RoomPasswordProvider)(nil)

// NewRoomPasswordProvider creates a RoomPasswordProvider from a
// campaign_id -> password mapping. passwords is not retained by
// reference beyond construction; the caller's map may be freely mutated
// or discarded afterward.
func NewRoomPasswordProvider(passwords map[string]string) *RoomPasswordProvider {
	owned := make(map[string]string, len(passwords))
	for campaignID, password := range passwords {
		owned[campaignID] = password
	}
	return &RoomPasswordProvider{passwords: owned}
}

// Authorize implements Provider. It never returns a non-nil error — a
// room password check has no failure mode short of a wrong/missing
// password, which is reported through ok/reason, not err.
func (p *RoomPasswordProvider) Authorize(_ context.Context, campaignID, authToken string) (bool, string, error) {
	want, configured := p.passwords[campaignID]
	if !configured {
		return true, "", nil
	}
	// Constant-time comparison: this is a password check, and campaign_id
	// values (unlike e.g. session tokens) are often guessable or
	// advertised (design doc's lobby-listing direction makes them
	// public by design), so the password itself is the only thing
	// standing between "anyone" and "authorized" — worth not leaking
	// timing information about it.
	if subtle.ConstantTimeCompare([]byte(authToken), []byte(want)) != 1 {
		return false, "incorrect password", nil
	}
	return true, "", nil
}
