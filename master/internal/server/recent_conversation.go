// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// recentConversationTurnLimit bounds how many of the most recent public
// narrative turns (narrative.player_bubble / broadcast client.display)
// feed recentConversationContextText — enough for the model to actually
// remember what just happened in the current scene, without letting the
// prompt grow without bound as a campaign goes on.
const recentConversationTurnLimit = 8

// recentConversationFetchLimit is how many raw events ListEvents pulls
// before filtering down to recentConversationTurnLimit narrative ones —
// generous, because most of what's actually recorded per turn is
// tool.result/roll.*/map.* bookkeeping, not narrative content itself.
const recentConversationFetchLimit = 60

// recentConversationContextText returns a "Recent conversation:" block
// for slowPassGroundingContext — the fix for a real, live-observed
// continuity failure: without this, every slow-pass turn was a fresh
// completion with no memory of the turn before it (this file's own
// existence closes the gap server.go's top-of-file doc comment used to
// flag directly: "no persistent context-assembly exists in Master to
// feed it"). Observed live: a player had their character tell an
// in-scene NPC "I'm looking to earn coin," with the model given no
// indication this was mid-conversation with a specific traveler met
// moments earlier — it reached for an unrelated, real, pack-authored
// tavern encounter instead of continuing the actual scene, grounded in
// genuine content, just the wrong content for what was actually
// happening, because nothing told the model what "actually happening"
// meant.
//
// Deliberately excludes any event with a private VisibilityScope
// (narrate_privately's asides, a character's own one-time creation
// intro, character_intro.go) — something whispered to one player must
// never leak into another player's later public turn just because
// Master's own event log technically has it (design doc §9.7). This is
// about the shared public thread carrying forward as memory, not a
// second, richer knowledge-scoping mechanism.
//
// Best-effort like every other context section slowPassGroundingContext
// assembles: no events store, no events recorded yet, or nothing
// narrative among what's recorded just means an empty section, never a
// failed turn.
func (s *Server) recentConversationContextText(ctx context.Context, campaignID string) string {
	if s.events == nil {
		return ""
	}
	events, _, err := s.events.ListEvents(ctx, campaignID, store.ListEventsOptions{Limit: recentConversationFetchLimit})
	if err != nil || len(events) == 0 {
		return ""
	}

	type turn struct {
		who  string
		text string
	}
	names := make(map[string]string)
	var turns []turn
	for _, ev := range events {
		switch protocol.MessageType(ev.MessageType) {
		case protocol.MessageTypeNarrativePlayerBubble:
			var msg protocol.NarrativePlayerBubbleMessage
			if err := json.Unmarshal(ev.Raw, &msg); err != nil || msg.Payload.Text == "" {
				continue
			}
			if isPrivateVisibility(msg.Payload.Visibility) {
				continue
			}
			turns = append(turns, turn{who: s.recentConversationSpeakerName(ctx, names, msg.Payload.CharacterID), text: msg.Payload.Text})
		case protocol.MessageTypeClientDisplay:
			var msg protocol.ClientDisplayMessage
			if err := json.Unmarshal(ev.Raw, &msg); err != nil || msg.Payload.Text == "" {
				continue
			}
			if isPrivateVisibility(msg.Payload.Visibility) {
				continue
			}
			turns = append(turns, turn{who: "DM", text: msg.Payload.Text})
		}
	}
	if len(turns) == 0 {
		return ""
	}
	if len(turns) > recentConversationTurnLimit {
		turns = turns[len(turns)-recentConversationTurnLimit:]
	}

	var b strings.Builder
	b.WriteString("Recent conversation (oldest first — what has actually already happened; continue from here, don't repeat or contradict it):\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "%s: %s\n", t.who, t.text)
	}
	return b.String()
}

// isPrivateVisibility reports whether v marks its message as scoped to
// specific characters only — shared by recentConversationContextText's
// two message-type cases.
func isPrivateVisibility(v *protocol.VisibilityScope) bool {
	return v != nil && v.Scope == protocol.VisibilityScopePrivate
}

// recentConversationSpeakerName resolves characterID to its display name
// for recentConversationContextText's transcript, caching in names (kept
// per call, not across calls) so a chatty scene doesn't re-fetch the
// same character repeatedly. Falls back to the raw ID when characters is
// unavailable or the lookup fails — a less readable label, but still a
// stable one the model can match against the party-roster section.
func (s *Server) recentConversationSpeakerName(ctx context.Context, names map[string]string, characterID string) string {
	if name, ok := names[characterID]; ok {
		return name
	}
	name := characterID
	if s.characters != nil {
		if character, err := s.characters.GetCharacter(ctx, characterID); err == nil {
			var data map[string]any
			if err := json.Unmarshal(character.CharacterData, &data); err == nil {
				if n, ok := data["name"].(string); ok && n != "" {
					name = n
				}
			}
		}
	}
	names[characterID] = name
	return name
}
