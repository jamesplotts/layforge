// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// CharacterStatus tracks where an uploaded character sits in design doc
// §9.4's review flow. The zero value, CharacterStatusUnspecified, is never
// valid on the wire — see IsValid. This is the Go translation of the
// Unspecified/LastValue enum-sentinel pattern from design doc §12 (see
// CLAUDE.md); Go has no enum range to bound with a LastValue, so IsValid's
// switch is the range check instead.
type CharacterStatus string

const (
	CharacterStatusUnspecified   CharacterStatus = ""
	CharacterStatusPendingReview CharacterStatus = "pending_review"
	CharacterStatusApproved      CharacterStatus = "approved"
	CharacterStatusRejected      CharacterStatus = "rejected"
)

// IsValid reports whether s is a recognized character status. It
// deliberately returns false for CharacterStatusUnspecified.
func (s CharacterStatus) IsValid() bool {
	switch s {
	case CharacterStatusPendingReview, CharacterStatusApproved, CharacterStatusRejected:
		return true
	default:
		return false
	}
}

// Character is one uploaded/imported character record.
type Character struct {
	// ID is Master's own identifier for this character, assigned on
	// upload — distinct from anything the system engine calls an
	// actor_id, which is opaque to Master (design doc §6.1: character_data
	// is only interpreted by the engine that owns schema_version).
	ID string

	// CampaignID scopes this character today. Design doc §9.4 describes
	// uploaded characters as personal library data keyed to the player's
	// account/Discord ID, snapshotted into campaign session state on
	// join, not campaign state itself. The account system now exists
	// (store.Account, Discord OAuth), but the library/snapshot split does
	// not — storing directly against CampaignID is a deliberate,
	// documented simplification pending that work, not the long-term shape.
	CampaignID string

	// OwnerID identifies the character's owner. On a Master running
	// Discord OAuth this is the authenticated account id
	// (store.Account.ID, e.g. "discord:8035..."); on one without it, the
	// client-declared sender_id, self-reported and not verified — the
	// same as every sender_id in this protocol. See
	// internal/server.actingSender for where the value is chosen.
	OwnerID string

	// SchemaVersion is the system engine schema_version CharacterData
	// conforms to (design doc §6.1).
	SchemaVersion string

	// Status is this character's position in the review flow (design doc
	// §9.4). Every character is created CharacterStatusPendingReview; the
	// operator moves it to Approved/Rejected from the admin panel (the
	// localhost operator surface — that is the privileged authorization,
	// the same one every other admin action relies on).
	Status CharacterStatus

	// CharacterData is the character's canonical JSON as the system
	// engine returned it (its ToJson/FromJson wire format), opaque to
	// Master beyond that it's valid per SchemaVersion.
	CharacterData json.RawMessage

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors returned by CharacterStore implementations. Callers that need to
// distinguish failure reasons should use errors.Is against these rather
// than comparing error strings.
var (
	ErrCharacterIDRequired = errors.New("store: character id is required")
	ErrCharacterNotFound   = errors.New("store: character not found")
)

// CharacterStore is Master's persistence for uploaded/imported characters
// (design doc §9.4). It deliberately does not import package protocol or
// package systemenginepb, for the same reason EventStore doesn't: it
// persists whatever CharacterData a caller hands it and returns it
// unchanged, leaving interpretation to whoever owns SchemaVersion.
type CharacterStore interface {
	// SaveCharacter durably records character, keyed by its ID — a save
	// using an ID that already exists overwrites that record (upsert),
	// matching how re-uploading the same character should behave. It
	// fails with ErrCampaignIDRequired or ErrCharacterIDRequired if
	// either field is empty.
	SaveCharacter(ctx context.Context, character Character) error

	// GetCharacter returns the character with the given ID, or
	// ErrCharacterNotFound if none exists.
	GetCharacter(ctx context.Context, characterID string) (Character, error)

	// ListCharacters returns every character (player-owned and NPC alike
	// — callers that need only real player characters filter on OwnerID
	// themselves, e.g. against the "master" sender_id create_npc saves
	// NPCs under) recorded for campaignID, in no particular guaranteed
	// order. Used by design doc §9.6's spotlight-balance tracking to know
	// the campaign's full character roster, not just whoever has spoken
	// recently. Returns an empty slice, not an error, for a campaign with
	// no characters at all.
	ListCharacters(ctx context.Context, campaignID string) ([]Character, error)

	// ListAllCharacters returns every character across every campaign,
	// newest first — the admin panel's cross-campaign roster view, which
	// (unlike ListCharacters and the WS-facing character tools) is not
	// scoped to one campaign. Returns an empty slice, not an error, when
	// there are none.
	ListAllCharacters(ctx context.Context) ([]Character, error)

	// MoveCharacter reassigns characterID to newCampaignID, carrying its
	// data/status/owner unchanged (an operator action — the design doc's
	// per-account snapshot model that would make this a copy is unbuilt;
	// see Character.CampaignID's doc comment). Fails with
	// ErrCharacterNotFound if no such character exists, or
	// ErrCampaignIDRequired if newCampaignID is empty.
	MoveCharacter(ctx context.Context, characterID, newCampaignID string) error

	// DeleteCharacter permanently removes characterID's record. Fails
	// with ErrCharacterNotFound if none exists. Deleting a character a
	// player is actively connected as leaves that connection with no
	// character until it re-uploads or reconnects — the admin UI warns
	// about this; the store just does what it's told.
	DeleteCharacter(ctx context.Context, characterID string) error
}
