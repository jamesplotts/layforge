// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// partyRosterContextText returns a best-effort DM-context section
// (design doc §8) listing every OTHER real player character in
// campaignID — excluding actingCharacterID (already given its own
// "Character ID:"/"Character data:" lines by runSlowPass) and excluding
// NPCs (create_npc saves those under masterSenderID, not a real player).
//
// Without this, the model has no way to reference a different player's
// character by a real ID within the same conversation — narrate_
// privately, apply_effect's PvP gate, and turn-order enforcement all
// become unreachable through natural conversation for that reason alone
// (see master/README.md's own "known, honestly-scoped gap" note from
// before this existed), not because those gates themselves are wrong;
// each is independently, thoroughly tested against the real handler
// code path regardless of whether a live model conversation can reach
// it.
//
// Each entry includes that character's own raw CharacterData, not just
// its ID — a live test against a real model proved ID-only isn't
// enough: told to privately address "my companion Bram" with no ID
// given, the model had no way to connect the name a player actually
// said to an opaque ID like "char-ally", and fell back to narrating
// everything publicly instead of ever calling narrate_privately. Once
// each ID carries its own data (which, in every real character this
// codebase has seen, includes a name field the model can read), it
// reliably makes that connection. This is the same "forward the raw
// JSON, let the model read whatever fields exist" posture the acting
// character's own "Character data:" line already uses — Master itself
// never parses or assumes a name field exists, only forwards what
// SaveCharacter already stored (design doc §6.1: character_data is
// opaque to Master beyond schema_version).
//
// Returns "" for any reason it can't produce a real answer (no
// characters store configured, or nobody else to list), the same
// best-effort shape as locationContextText/spotlightContextText.
func (s *Server) partyRosterContextText(ctx context.Context, campaignID, actingCharacterID string) string {
	if s.characters == nil {
		return ""
	}
	characters, err := s.characters.ListCharacters(ctx, campaignID)
	if err != nil {
		return ""
	}

	var others []store.Character
	for _, c := range characters {
		if c.ID == actingCharacterID {
			continue
		}
		if c.OwnerID == "" || c.OwnerID == masterSenderID {
			continue
		}
		others = append(others, c)
	}
	if len(others) == 0 {
		return ""
	}
	// ListCharacters makes no ordering guarantee — sorted so this
	// section (and any test asserting on it) doesn't flap between
	// otherwise-identical turns.
	sort.Slice(others, func(i, j int) bool { return others[i].ID < others[j].ID })

	var b strings.Builder
	b.WriteString("Other real player characters in this campaign (use these exact IDs for any tool call or private narration targeting one of them — never invent one):\n")
	for _, c := range others {
		fmt.Fprintf(&b, "- %s: %s\n", c.ID, c.CharacterData)
	}
	return b.String()
}
