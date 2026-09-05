// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Pregen is one Host/DM-authored pregenerated character template
// (design doc §9.4's "pick one the Host offers" join-time option). A
// pregen is never itself played from — it's a template, always copied
// into a brand-new Character (with a fresh ID and the claiming player's
// real OwnerID) the moment a player selects it; the template row is
// never mutated by a claim.
type Pregen struct {
	ID            string
	CampaignID    string
	Name          string
	Description   string
	SchemaVersion string
	CharacterData json.RawMessage
	CreatedAt     time.Time
}

// ErrPregenIDRequired is returned by SavePregen when ID is empty.
var ErrPregenIDRequired = errors.New("store: pregen id is required")

// ErrPregenNotFound is returned by GetPregen when no pregen with the
// given ID exists.
var ErrPregenNotFound = errors.New("store: pregen not found")

// PregenStore is Master's persistence for Host/DM-authored pregenerated
// characters (design doc §9.4), backing the admin panel's Pregens tab
// and the join-time "pick a pregen" option. Implemented by
// SQLiteEventStore alongside CampaignPackStore/VehicleStore — its own
// table, same one-table-per-concern pattern.
type PregenStore interface {
	// SavePregen creates or replaces (upsert, keyed by ID) one pregen.
	// Fails with ErrCampaignIDRequired or ErrPregenIDRequired if either
	// field is empty.
	SavePregen(ctx context.Context, pregen Pregen) error

	// GetPregen returns the pregen with the given ID, or
	// ErrPregenNotFound if none exists.
	GetPregen(ctx context.Context, pregenID string) (Pregen, error)

	// ListPregens returns every pregen bound to campaignID, in no
	// particular guaranteed order. Empty slice, not an error, for a
	// campaign with none.
	ListPregens(ctx context.Context, campaignID string) ([]Pregen, error)

	// DeletePregen permanently removes one pregen. Deleting a pregen
	// that's already been claimed by a player has no effect on that
	// player's own Character row — claiming always copies, it never
	// links back to the template.
	DeletePregen(ctx context.Context, pregenID string) error
}
