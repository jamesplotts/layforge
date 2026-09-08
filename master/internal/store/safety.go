// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import "context"

// SafetyStore is Master's persistence for design doc §9.2's safety
// tools. When a player invokes the safety tool with a topic
// (`safety.flag`), that topic becomes a standing "do not narrate this
// going forward" constraint for the rest of the campaign — so it has to
// outlive a Master restart rather than living only in memory, the same
// reasoning CombatStateStore already applies to turn order and fog of
// war. A topicless flag is only a scene interrupt with nothing to
// persist; the caller handles that case and never reaches this store.
//
// Pre-declared campaign-pack lines/veils (design doc §6.4 front matter)
// are the *other* source of standing constraints and are NOT stored
// here — they're read straight from the bound pack every turn, since
// they can't change during play.
//
// Implemented by SQLiteEventStore the same way EventStore/CharacterStore
// already are.
type SafetyStore interface {
	// AddSafetyFlag records topic as a standing safety constraint for
	// campaignID. Idempotent: re-flagging a topic already recorded for
	// this campaign is a no-op, not an error. Fails with
	// ErrCampaignIDRequired if campaignID is empty; topic must be
	// non-empty (guaranteed by the caller).
	AddSafetyFlag(ctx context.Context, campaignID, topic string) error

	// ListSafetyFlags returns every topic flagged for campaignID, in the
	// order they were first flagged (oldest first). An empty slice, not
	// an error, when the campaign has none.
	ListSafetyFlags(ctx context.Context, campaignID string) ([]string, error)
}
