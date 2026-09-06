// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"errors"

	"github.com/jamesplotts/layforge/master/internal/terms"
)

// adminSettingsKeyTermsAcceptedVersion mirrors package admin's own
// unexported-in-spirit (exported, but conceptually admin's) SystemKeyTermsAcceptedVersion
// constant (internal/admin/server.go) — the key its /api/terms endpoints
// read and write. Duplicated here rather than imported: importing
// package admin from package server would invert the real dependency
// direction (main.go wires admin on top of server's own interfaces, via
// store.AdminSettingsStore, not the other way around) — the same
// reasoning master/internal/registry/heartbeat.go's own
// npcOwnerSenderID duplication already documents for this codebase.
const adminSettingsKeyTermsAcceptedVersion = "terms_accepted_version"

// connState holds one WebSocket connection's own in-memory-only state —
// today just termsAccepted, but the natural place for any future
// per-connection (not per-campaign, not per-sender_id) flag. Created
// fresh in serve for each new connection and never persisted:
// sender_id is entirely client-declared with no account system behind
// it (nothing server-side verifies "this is the same human as last
// time"), so persisting acceptance against that string would be false
// confidence — gating the live connection itself is the real trust
// boundary already relied on everywhere else in this protocol, and it
// costs a compliant client nothing beyond resending terms.accept once
// per connection.
type connState struct {
	termsAccepted bool
}

// termsGateError pairs a system.error Code with the human-readable
// cause dispatch's terms gate should send — checkTermsAccepted's own
// return type, so its two distinct failure reasons (operator vs. this
// connection) each get the right machine-readable Code.
type termsGateError struct {
	code string
	err  error
}

// checkTermsAccepted enforces terms.go's own gate (see connState's doc
// comment for why this is a live-connection check, not a persisted
// one): nil if the Host has accepted the current terms.Version and this
// connection has too, otherwise a termsGateError identifying which of
// the two is unmet. s.adminSettings == nil disables the gate entirely
// (every message passes) — the same "nil means this feature isn't
// configured for this deployment" pattern every other optional
// dependency on Server already uses; in production main.go always
// passes a real value, so this only matters for the many existing tests
// in this package that pass nil for every store dependency they don't
// care about and shouldn't need to plumb a terms-acceptance fixture
// through just to keep exercising unrelated behavior.
func (s *Server) checkTermsAccepted(ctx context.Context, cs *connState) *termsGateError {
	if s.adminSettings == nil {
		return nil
	}
	settings, err := s.adminSettings.GetSystemSettings(ctx)
	if err != nil || settings[adminSettingsKeyTermsAcceptedVersion] != terms.Version {
		return &termsGateError{code: "operator_terms_not_accepted", err: errors.New("the Host running this campaign hasn't completed setup yet")}
	}
	if !cs.termsAccepted {
		return &termsGateError{code: "player_terms_not_accepted", err: errors.New("you must accept the terms before continuing — send terms.accept first")}
	}
	return nil
}
