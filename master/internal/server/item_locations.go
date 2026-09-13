// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// itemLocationsContextText returns a best-effort DM-context section
// (design doc §8) listing where each of actingCharacterID's carried
// items currently is — equipped, stowed in a specific container, or
// loose/quick-access — computed by a real ListCarriedItems engine call,
// never guessed or inferred from character_data (design doc §6.1:
// character_data is opaque to Master beyond schema_version).
//
// Without this, the DM model has no way to know whether an item it was
// told went "into the pack" is actually still there, back on a belt, or
// dropped — the real, live-observed continuity bug (a crowbar narrated
// as stowed, then later "at his hip") this section, and the pack_item/
// draw_item tools it's paired with, exist to close.
//
// Scoped to the acting character only, the same "Character data:" scope
// runSlowPass already uses — not the whole party roster
// (partyRosterContextText's own reasoning doesn't apply here: a tool
// call always names one specific character's items, never another
// player's). Gated like mechanicsTools() (both systemEngine and
// characters configured), not the looser single condition
// partyRosterContextText uses, since this makes a real engine call that
// would otherwise fail on every turn without one.
//
// Returns "" for any reason it can't produce a real answer — no system
// engine/characters configured, no acting character, actingCharacterID
// not found, the engine call itself failing, or the character carrying
// nothing at all — the same best-effort shape every other *ContextText
// helper already uses.
func (s *Server) itemLocationsContextText(ctx context.Context, campaignID, actingCharacterID string) string {
	if s.systemEngine == nil || s.characters == nil {
		return ""
	}
	if actingCharacterID == "" {
		return ""
	}

	character, err := s.campaignCharacter(ctx, campaignID, actingCharacterID)
	if err != nil {
		return ""
	}
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return ""
	}

	resp, err := s.systemEngine.ListCarriedItems(ctx, &systemenginepb.ListCarriedItemsRequest{
		RequestId: "context-" + character.ID,
		Actor:     &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	})
	// resp == nil alongside a nil err should never happen against a real
	// gRPC client (a call either returns a response or a non-nil error),
	// but checking it explicitly rather than assuming it — the same
	// defensive posture as every nil check surrounding this one — is the
	// difference between "" and a panic inside a goroutine whose only
	// recover() is runSlowPass's own, which would otherwise swallow a
	// real, live-observed failure shape: several tool-heavy tests fed a
	// bare *fakeSystemEngineClient{} (no ListCarriedItems stub at all)
	// hung waiting for a bubble that a silent panic here had already
	// eaten, well before any assertion ran.
	if err != nil || resp == nil || !resp.Success || len(resp.Items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Item locations:\n")
	for _, item := range resp.Items {
		fmt.Fprintf(&b, "- %s: %s\n", item.ItemName, item.LocationDescription)
	}
	return b.String()
}
