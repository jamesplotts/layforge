// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"encoding/json"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// characterDataStringField reads a single top-level string field out of
// character's opaque character_data — the shared best-effort extraction
// characterDisplayName/characterRace/characterGender below all build on,
// rather than each re-unmarshaling the document and re-deriving the same
// "missing, wrong type, or empty means unavailable" handling. character_data
// is engine-defined and opaque to Master beyond schema_version (design doc
// §6.1), so ok is false for any reason the field isn't usable — a
// character predating the field, a non-OpenCombatEngine schema, or
// malformed data — never an error a caller needs to handle specially.
func characterDataStringField(character store.Character, field string) (value string, ok bool) {
	var data map[string]any
	if err := json.Unmarshal(character.CharacterData, &data); err != nil {
		return "", false
	}
	v, isString := data[field].(string)
	if !isString || v == "" {
		return "", false
	}
	return v, true
}

// characterDisplayName returns character's own display name — the
// "name" field inside character_data, when present — falling back to
// its store ID otherwise. Any real character this codebase has seen
// carries a name field, but nothing guarantees one exists.
//
// Shared by every place the DM or the narrative pipeline needs something
// readable to call a character instead of its opaque ID: an
// incapacitated-turn skip announcement (turn_order.go), the fast pass's
// own rendering (renderPlayerBubble, server.go), and the recent-
// conversation transcript (recent_conversation.go).
func characterDisplayName(character store.Character) string {
	if name, ok := characterDataStringField(character, "name"); ok {
		return name
	}
	return character.ID
}

// characterRace returns character's own SRD race name ("Dwarf", "Elf",
// "Human", "Halfling", ...) from character_data's raceName field
// (OpenCombatEngine's CreatureState.RaceName — see character-sheet.js's
// own RACE_ADJECTIVES for the client-side sibling of this lookup), or ""
// when unset — every DM-authored monster/NPC, and any character rolled
// before that field existed. Used by the fast pass so it can pick a
// race-appropriate noun instead of a generic one: in D&D terms "man"/
// "woman" specifically mean Human, so using either for a Dwarf or an Elf
// silently misstates their race.
func characterRace(character store.Character) string {
	race, _ := characterDataStringField(character, "raceName")
	return race
}

// characterGender returns character's own player-chosen gender ("Male"/
// "Female" — OpenCombatEngine's CreatureState.Gender, purely cosmetic
// roleplay flavor with no mechanical effect) from character_data's
// gender field, or "" when unset. Used by the fast pass so its pronoun
// choice is a real fact instead of a guess from the character's name.
func characterGender(character store.Character) string {
	gender, _ := characterDataStringField(character, "gender")
	return gender
}
