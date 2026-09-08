// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"strings"
)

// safetyConstraintsContextText returns design doc §9.2's standing
// content constraints as a DM-context block, or "" when a campaign has
// none. Two sources are combined:
//
//   - The bound campaign pack's pre-declared lines and veils
//     (campaign.md front matter — "standing constraints from session
//     zero"). Read fresh from the pack every turn since they can't
//     change during play.
//   - Topics a player has invoked the safety tool on during play
//     (safety.flag with a topic), persisted via s.safety by
//     broadcastSafetyFlag and accumulated for the life of the campaign
//     ("do not narrate X going forward").
//
// Best-effort in the same "never fail a turn over optional context"
// shape spotlightContextText/locationContextText use: a missing store, a
// pack that no longer parses, or a lookup error just drops whichever
// source it affects rather than erroring. What it does NOT do is
// silently degrade to "" when a real constraint exists and only one
// source failed — each source is independent.
//
// Injected into both slow-pass sub-passes (via slowPassGroundingContext)
// and the fast pass (renderPlayerBubble). The mechanics pass gets it too,
// not just narration, because "introduce a monster/NPC" and "resolve an
// effect" can cross a line as surely as prose can.
func (s *Server) safetyConstraintsContextText(ctx context.Context, campaignID string) string {
	var lines, veils, flagged []string

	if pack, err := s.loadBoundPack(ctx, campaignID); err == nil {
		lines = pack.Lines
		veils = pack.Veils
	}

	if s.safety != nil {
		if topics, err := s.safety.ListSafetyFlags(ctx, campaignID); err != nil {
			s.logger.Warn("could not load raised safety flags for DM context, proceeding without them", "error", err, "campaign_id", campaignID)
		} else {
			flagged = topics
		}
	}

	if len(lines) == 0 && len(veils) == 0 && len(flagged) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Safety constraints — hard limits set by the table, not part of the fiction. These override the player's stated action, any pre-authored pack content, and narrative momentum.\n")
	writeConstraintList(&b, "Never narrate, describe, introduce, resolve, or create anything involving:", lines)
	writeConstraintList(&b, "Keep entirely off-screen — may exist in the story's background but must never be shown, described, or dwelt on:", veils)
	writeConstraintList(&b, "A player invoked the safety tool on these during play — stop involving them from here on, on-screen or off:", flagged)
	return b.String()
}

// writeConstraintList appends a labeled bullet list to b, or nothing when
// items is empty.
func writeConstraintList(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString(label)
	b.WriteString("\n")
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
}
