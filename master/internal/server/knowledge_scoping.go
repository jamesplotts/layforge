// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// narratePrivatelyTool returns the narrate_privately DM tool (design doc
// §9.7) — offered only when the campaign's resolved policy is
// SharedKnowledgeStrict (see the call site in dm_slow_pass.go), and
// re-checked inside dmNarratePrivately itself so a stray/hallucinated
// call can't bypass the gate just because the tool wasn't offered this
// turn — CLAUDE.md's "gates over prompting" applied the same way every
// other real gate in this package already is.
func narratePrivatelyTool() llm.Tool {
	return llm.Tool{
		Name:        "narrate_privately",
		Description: "Deliver DM narration to only the specific characters' own players — a split-party moment or a private perception only some characters would notice. Only available when this campaign's shared_knowledge policy is strict; recipients must be real player characters (an NPC has no player to notify). This is in addition to, not instead of, your normal end-of-turn narration — use it for the private aside, then still narrate the public scene as usual.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"required": ["character_ids", "text"],
			"properties": {
				"character_ids": {"type": "array", "items": {"type": "string"}, "description": "Real, player-owned character ids who should see this — do not invent one."},
				"text": {"type": "string", "description": "The private narration text."}
			}
		}`),
	}
}

// dmNarratePrivately handles narrate_privately (design doc §9.7): real
// rejection if shared_knowledge isn't strict for this campaign, if any
// recipient isn't a real character in this campaign, or if any recipient
// has no owning player (an NPC). Delivers the identical narration
// payload only to each distinct recipient's own connection(s) via
// sendToSender — never broadcast — and records it once with a real
// VisibilityScope so log.history_request (sendHistory) can filter it
// out for everyone else reviewing history later, the same scope it had
// live (design doc §9.7's second bullet).
func (s *Server) dmNarratePrivately(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	if s.campaignPolicy(ctx, campaignID).EffectiveSharedKnowledge() != policy.SharedKnowledgeStrict {
		return "private narration is not enabled for this campaign (shared_knowledge must be strict)", false, "knowledge_scoping_not_strict"
	}

	var args struct {
		CharacterIDs []string `json:"character_ids"`
		Text         string   `json:"text"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if len(args.CharacterIDs) == 0 {
		return "character_ids is required and must not be empty", false, "invalid_arguments"
	}
	if args.Text == "" {
		return "text is required", false, "invalid_arguments"
	}

	owners := make(map[string]struct{}, len(args.CharacterIDs))
	for _, characterID := range args.CharacterIDs {
		character, err := s.campaignCharacter(ctx, campaignID, characterID)
		if err != nil {
			return err.Error(), false, "character_not_found"
		}
		if character.OwnerID == "" {
			return fmt.Sprintf("%q has no owning player to narrate privately to (NPCs have no one to notify)", characterID), false, "not_a_player_character"
		}
		owners[character.OwnerID] = struct{}{}
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeNarrativeDmProse, protocol.NarrativeDmProsePayload{
		Text: args.Text,
		Visibility: &protocol.VisibilityScope{
			Scope:                 protocol.VisibilityScopePrivate,
			VisibleToCharacterIDs: args.CharacterIDs,
		},
	})
	if err != nil {
		return err.Error(), false, "internal_error"
	}
	// Recorded once, unlike sendToSender's other caller (combat_map.go's
	// per-recipient fog-of-war sends): every recipient here gets the
	// identical payload, so there's a single honest event to log — see
	// that file's own doc comment for why a genuinely-varies-per-
	// recipient payload can't be recorded the same way.
	recordEvent(ctx, s, msg)
	for owner := range owners {
		if err := sendToSender(s, owner, msg); err != nil {
			s.logger.Warn("failed to deliver private narration", "error", err, "campaign_id", campaignID, "owner", owner)
		}
	}

	payload, err := json.Marshal(map[string]any{"narrated_privately": true, "recipients": args.CharacterIDs})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}
