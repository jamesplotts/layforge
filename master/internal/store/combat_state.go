// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import "context"

// CombatStateStore is Master's persistence for a campaign's active
// combat state — turn order/initiative (package server's turnOrder) and
// combat-map/fog-of-war state (package server's combatMapMeta), design
// doc §3.1, §9.3, §6.2. Both were previously in-memory-only, explicitly
// documented as lost on a Master restart; this closes that gap.
// Implemented by SQLiteEventStore the same way CharacterStore/EventStore
// already are.
//
// This package deliberately does not import package server (see store.go's
// package doc comment for the same "persist whatever the caller hands it"
// reasoning EventStore/CharacterStore already follow) — payload is
// caller-serialized JSON, opaque to this package.
type CombatStateStore interface {
	// SaveCombatState upserts campaignID's combat-state snapshot.
	SaveCombatState(ctx context.Context, campaignID string, payload []byte) error

	// LoadCombatState returns campaignID's stored combat-state snapshot.
	// ok is false (with a nil payload) when nothing has ever been saved
	// for this campaign, or it was already removed by DeleteCombatState
	// (e.g. when combat ended) — callers should treat that as "no active
	// combat," not an error.
	LoadCombatState(ctx context.Context, campaignID string) (payload []byte, ok bool, err error)

	// DeleteCombatState removes campaignID's stored combat-state
	// snapshot, if any. Safe to call when none exists.
	DeleteCombatState(ctx context.Context, campaignID string) error

	// ListCombatStateCampaignIDs returns every campaign_id with a
	// currently-stored combat-state snapshot — used once, at Master
	// startup, to know which campaigns need their in-memory turn-order/
	// combat-map state rehydrated.
	ListCombatStateCampaignIDs(ctx context.Context) ([]string, error)
}
