// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"encoding/json"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// characterDisplayName returns character's own display name — the
// "name" field inside its opaque character_data, when present — falling
// back to its store ID otherwise. character_data is engine-defined and
// opaque to Master beyond schema_version (design doc §6.1), so this is
// necessarily best-effort: any real character this codebase has seen
// carries a name field, but nothing guarantees one exists.
//
// Shared by every place the DM or the narrative pipeline needs something
// readable to call a character instead of its opaque ID: an
// incapacitated-turn skip announcement (turn_order.go), the fast pass's
// own rendering (renderPlayerBubble, server.go), and the recent-
// conversation transcript (recent_conversation.go).
func characterDisplayName(character store.Character) string {
	name := character.ID
	var data map[string]any
	if err := json.Unmarshal(character.CharacterData, &data); err == nil {
		if n, ok := data["name"].(string); ok && n != "" {
			name = n
		}
	}
	return name
}
