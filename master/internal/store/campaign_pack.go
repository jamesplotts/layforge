// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"time"
)

// CampaignPack records which pack directory (if any) is bound to a
// campaign (design doc §6.4) — package campaignpack's own LoadPack does
// the actual parsing; this just remembers which directory to load next
// time and when it was last (re)bound.
type CampaignPack struct {
	CampaignID string
	PackDir    string
	PackID     string
	LoadedAt   time.Time
}

// LocationState is one location's mutable per-campaign state —
// static content (id, connections, description) comes from
// package campaignpack instead; this is only what changes during play.
type LocationState struct {
	CampaignID     string
	LocationID     string
	Discovered     bool
	ClaimedByParty bool
	ClaimNote      string
}

// StashedItem is one item instance a character left at a location —
// one row per item, matching the system engine's own no-stacking
// inventory convention (protocol/system_engine.proto's
// AddItemToInventoryRequest doc comment).
type StashedItem struct {
	ID          string
	CampaignID  string
	LocationID  string
	CharacterID string
	ItemName    string
	CreatedAt   time.Time
}

// CampaignPackStore is Master's persistence for campaign-pack binding
// and the mutable session state a loaded pack's locations need (design
// doc §6.4, §10) — which pack directory is bound to a campaign, the
// party's current location, per-location discovered/claimed state, and
// off-site possessions (stashed items/currency). Implemented by
// SQLiteEventStore the same way CombatStateStore/CharacterStore already
// are.
type CampaignPackStore interface {
	// SaveCampaignPack binds packDir (already validated as a real,
	// parseable pack — see campaignpack.LoadPack — by the caller) to
	// campaignID, replacing any previous binding.
	SaveCampaignPack(ctx context.Context, campaignID, packDir, packID string) error

	// GetCampaignPack returns campaignID's bound pack directory. ok is
	// false when no pack has ever been bound.
	GetCampaignPack(ctx context.Context, campaignID string) (pack CampaignPack, ok bool, err error)

	// GetPartyLocation returns campaignID's current party location id,
	// or "" if the party hasn't traveled anywhere yet (the bootstrap
	// case package server's travel_to DM tool handles specially).
	GetPartyLocation(ctx context.Context, campaignID string) (locationID string, err error)

	// SetPartyLocation records campaignID's new current party location.
	SetPartyLocation(ctx context.Context, campaignID, locationID string) error

	// GetLocationState returns locationID's mutable state within
	// campaignID. ok is false (with a zero LocationState) when nothing
	// has ever been recorded for it — callers should treat that the
	// same as {Discovered: false, ClaimedByParty: false}, not an error.
	GetLocationState(ctx context.Context, campaignID, locationID string) (state LocationState, ok bool, err error)

	// SetLocationDiscovered marks locationID as discovered within
	// campaignID — idempotent, and never un-discovers a location
	// already marked.
	SetLocationDiscovered(ctx context.Context, campaignID, locationID string) error

	// SetLocationClaimed marks locationID as claimed by the party
	// (land holdings) within campaignID, recording note alongside it.
	SetLocationClaimed(ctx context.Context, campaignID, locationID, note string) error

	// StashItem records one item instance (itemName) left by
	// characterID at locationID within campaignID. id is the caller-
	// generated row identifier (package server's newRandomID) — this
	// package never generates identifiers itself, matching
	// store.Character.ID's own existing convention.
	StashItem(ctx context.Context, id, campaignID, locationID, characterID, itemName string) error

	// RetrieveItem removes exactly one matching stashed item instance
	// (itemName, at locationID, left by characterID, within
	// campaignID). found is false when no matching row exists — a real
	// "nothing stashed here" rejection for the caller to report, not an
	// error.
	RetrieveItem(ctx context.Context, campaignID, locationID, characterID, itemName string) (found bool, err error)

	// ListStashedItems returns every item instance stashed at
	// locationID within campaignID, across every character —
	// list_locations (package server) uses this for the same "full
	// enumeration, not an abstract summary" reasoning
	// list_vendor_inventory already established.
	ListStashedItems(ctx context.Context, campaignID, locationID string) ([]StashedItem, error)

	// AddStashedCurrency deposits currency (a real, mechanical action —
	// package server's stash_currency DM tool removes it from the
	// depositing character's own inventory first via a real system-
	// engine call) into locationID's stash for characterID within
	// campaignID — additive, not a replace: depositing twice
	// accumulates rather than overwriting, the one upsert in this
	// package that behaves this way (every other SaveXxx method here
	// and elsewhere in this package fully replaces).
	AddStashedCurrency(ctx context.Context, campaignID, locationID, characterID string, copper, silver, gold, platinum int32) error

	// RemoveStashedCurrency withdraws currency from locationID's stash
	// for characterID within campaignID — a real rejection
	// (ErrInsufficientStashedCurrency) if any single requested
	// denomination exceeds what's actually stashed, mirroring the
	// system engine's own TransferCurrency behavior (no change-making
	// across denominations).
	RemoveStashedCurrency(ctx context.Context, campaignID, locationID, characterID string, copper, silver, gold, platinum int32) error

	// GetStashedCurrency returns characterID's currently stashed
	// currency at locationID within campaignID — all zero when nothing
	// has ever been deposited, not an error.
	GetStashedCurrency(ctx context.Context, campaignID, locationID, characterID string) (copper, silver, gold, platinum int32, err error)
}
