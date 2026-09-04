// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"time"
)

// Vehicle is a real, named mount/cart/wagon/ship (or any other
// conveyance) tracked for a campaign — off-site possessions' other
// named half, alongside stashed items/currency (design doc §6.4's
// "off-site possessions (mounts, stashes)" line). Deliberately not a
// character/creature record even for an animal mount: this package
// tracks only where a vehicle is, not mechanical stats — a mount that
// needs real combat stats (AC, HP, speed) is still created as an
// ordinary character via create_npc/FromJson, the same as any other
// creature; this is who owns/uses it and where it currently is, which
// the character/creature schema has no concept of.
type Vehicle struct {
	ID          string
	CampaignID  string
	Name        string
	VehicleType string
	// Stabled is false while the vehicle is traveling with the party —
	// the default state a newly acquired vehicle starts in. true means
	// it's left behind at LocationID until retrieved.
	Stabled bool
	// LocationID is only meaningful when Stabled is true.
	LocationID string
	CreatedAt  time.Time
}

// VehicleStore is Master's persistence for a campaign's real vehicles
// (design doc §6.4) — implemented by SQLiteEventStore the same way
// CampaignPackStore/CombatStateStore already are.
type VehicleStore interface {
	// CreateVehicle records a new vehicle (id is the caller-generated
	// row identifier, package server's newRandomID — this package never
	// generates identifiers itself, matching store.Character.ID's own
	// existing convention), starting in the traveling-with-the-party
	// state (Stabled = false).
	CreateVehicle(ctx context.Context, id, campaignID, name, vehicleType string) error

	// ListVehicles returns every vehicle that exists for campaignID,
	// stabled or traveling — list_vehicles (package server) uses this
	// for the same "full enumeration, not an abstract summary" reasoning
	// list_locations/list_npcs/list_vendor_inventory already established.
	ListVehicles(ctx context.Context, campaignID string) ([]Vehicle, error)

	// GetVehicle returns one vehicle by id, scoped to campaignID — ok is
	// false when it doesn't exist or belongs to a different campaign.
	GetVehicle(ctx context.Context, campaignID, vehicleID string) (vehicle Vehicle, ok bool, err error)

	// StableVehicle marks vehicleID as stabled at locationID within
	// campaignID. found is false when vehicleID doesn't exist in this
	// campaign — a real rejection, not a silent no-op.
	StableVehicle(ctx context.Context, campaignID, vehicleID, locationID string) (found bool, err error)

	// TakeVehicle marks vehicleID as traveling with the party again
	// (Stabled = false) within campaignID. found is false when
	// vehicleID doesn't exist in this campaign.
	TakeVehicle(ctx context.Context, campaignID, vehicleID string) (found bool, err error)
}
