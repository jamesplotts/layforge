// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// seedCharacterWithStatus is seedCharacter (roll_check_test.go) with a
// caller-chosen store.CharacterStatus — these tests are specifically
// about ownedCharacter's requireApproved gate (design doc §9.4), so they
// need to control status directly rather than always getting Approved.
func seedCharacterWithStatus(t *testing.T, st *store.SQLiteEventStore, id, campaignID, ownerID string, status store.CharacterStatus) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.SaveCharacter(context.Background(), store.Character{
		ID:            id,
		CampaignID:    campaignID,
		OwnerID:       ownerID,
		SchemaVersion: "opencombatengine-v1",
		Status:        status,
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
}

func TestServe_RollCheckRequest_PendingReviewCharacter_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacterWithStatus(t, st, "char-1", "campaign-roll", "player-a", store.CharacterStatusPendingReview)

	conn := dialAndJoin(t, ts, "campaign-roll", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-roll", "player-a", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, data, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (character is still pending review)", typ, protocol.MessageTypeSystemError)
	}
	var errMsg protocol.SystemErrorMessage
	if err := json.Unmarshal(data, &errMsg); err != nil {
		t.Fatalf("unmarshaling system.error: %v", err)
	}
	if errMsg.Payload.Message == "" {
		t.Error("system.error Message is empty, want an explanation")
	}
}

func TestServe_RollCheckRequest_RejectedCharacter_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacterWithStatus(t, st, "char-1", "campaign-roll", "player-a", store.CharacterStatusRejected)

	conn := dialAndJoin(t, ts, "campaign-roll", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-roll", "player-a", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (character was rejected)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_CharacterGet_PendingReviewCharacter_StillSucceeds(t *testing.T) {
	fake := &fakeSystemEngineClient{
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacterWithStatus(t, st, "char-1", "campaign-view", "player-a", store.CharacterStatusPendingReview)

	conn := dialAndJoin(t, ts, "campaign-view", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterGet(ctx, conn, "player-a", "campaign-view", "char-1"); err != nil {
		t.Fatalf("requestCharacterGet() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	// A player can always view their own character's sheet while it's
	// under review — only mechanical actions (rolling, applying effects,
	// moving) are gated. See ownedCharacter's requireApproved doc comment.
	if typ != protocol.MessageTypeCharacterState {
		t.Fatalf("response type = %q, want %q (viewing a pending character must still work)", typ, protocol.MessageTypeCharacterState)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CastSpell_PendingReviewCaster_ReturnsFailureToolResult
// proves characterMayAct's gate is real at the DM-tool dispatch layer,
// not just ownedCharacter's player-initiated half: the DM model narrates
// and calls cast_spell for a character that hasn't cleared review yet,
// and the call must fail — the model routing around
// roll.check_request/character.apply_effect's own gate by acting through
// a DM tool call instead must not work either.
func TestServe_NarrativePlayerInput_SlowPass_CastSpell_PendingReviewCaster_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := castSpellToolCallLLM(`{"character_id":"caster-char","spell_name":"Magic Missile","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacterWithStatus(t, st, "caster-char", "campaign-cast-pending", "player-a", store.CharacterStatusPendingReview)
	seedCharacter(t, st, "target-char", "campaign-cast-pending", "master")

	conn := dialAndJoin(t, ts, "campaign-cast-pending", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-cast-pending", "player-a", "caster-char", "I cast Magic Missile at it!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false (caster is still pending review)")
	}
	if toolResult.Payload.ReasonCode != "character_not_approved" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "character_not_approved")
	}
	if fakeEngine.lastCastSpellRequest != nil {
		t.Error("CastSpell was called on the system engine, want the gate to reject before ever reaching it")
	}
}
