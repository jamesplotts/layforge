// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func requestApplyEffect(ctx context.Context, conn *websocket.Conn, sender, campaignID, characterID string, effect map[string]any) error {
	effectJSON, err := json.Marshal(effect)
	if err != nil {
		return err
	}
	msg := protocol.CharacterApplyEffectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "apply-effect-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterApplyEffect,
		},
		Payload: protocol.CharacterApplyEffectPayload{CharacterID: characterID, Effect: effectJSON},
	}
	return wsjson.Write(ctx, conn, msg)
}

func TestServe_CharacterApplyEffect_OwnedCharacter_PersistsAndReturnsUpdatedState(t *testing.T) {
	updatedData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "hitPoints": map[string]any{"current": 15.0, "max": 20.0}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		applyEffectResp: &systemenginepb.ApplyEffectResponse{
			Success: true,
			Actor: &systemenginepb.Actor{
				ActorId:       "char-1",
				CharacterData: updatedData,
				SchemaVersion: "opencombatengine-v1",
			},
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{
			Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE,
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-effect", "player-a")

	conn := dialAndJoin(t, ts, "campaign-effect", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestApplyEffect(ctx, conn, "player-a", "campaign-effect", "char-1", map[string]any{
		"effectType": "damage",
		"amount":     5,
	}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}

	var resp protocol.CharacterStateMessage
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read(character.state) error = %v", err)
	}
	if resp.Payload.Status != "active" {
		t.Errorf("Status = %q, want %q", resp.Payload.Status, "active")
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Payload.CharacterData, &data); err != nil {
		t.Fatalf("unmarshaling CharacterData error = %v", err)
	}
	hp, _ := data["hitPoints"].(map[string]any)
	if hp["current"] != 15.0 {
		t.Errorf("CharacterData.hitPoints.current = %v, want 15", hp["current"])
	}

	// The engine's response must actually be persisted, not just echoed
	// back for this one reply.
	saved, err := st.GetCharacter(ctx, "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	var savedData map[string]any
	if err := json.Unmarshal(saved.CharacterData, &savedData); err != nil {
		t.Fatalf("unmarshaling saved CharacterData error = %v", err)
	}
	savedHP, _ := savedData["hitPoints"].(map[string]any)
	if savedHP["current"] != 15.0 {
		t.Errorf("persisted CharacterData.hitPoints.current = %v, want 15", savedHP["current"])
	}

	if fake.lastApplyEffectRequest.Effect.Fields["effectType"].GetStringValue() != "damage" {
		t.Errorf("ApplyEffect called with effect.effectType = %v, want %q", fake.lastApplyEffectRequest.Effect.Fields["effectType"], "damage")
	}
	if fake.lastApplyEffectRequest.Effect.Fields["amount"].GetNumberValue() != 5 {
		t.Errorf("ApplyEffect called with effect.amount = %v, want 5", fake.lastApplyEffectRequest.Effect.Fields["amount"])
	}
}

func TestServe_CharacterApplyEffect_EngineReportsFailure_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{
		applyEffectResp: &systemenginepb.ApplyEffectResponse{Success: false, Error: "unknown effectType"},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-effect", "player-a")

	conn := dialAndJoin(t, ts, "campaign-effect", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestApplyEffect(ctx, conn, "player-a", "campaign-effect", "char-1", map[string]any{"effectType": "bogus"}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}

	// A failed apply must not have persisted anything.
	saved, err := st.GetCharacter(ctx, "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(saved.CharacterData, &data); err != nil {
		t.Fatalf("unmarshaling CharacterData error = %v", err)
	}
	if data["name"] != "Kestrel" {
		t.Errorf("CharacterData changed after a failed apply: %v", data)
	}
}

func TestServe_CharacterApplyEffect_CharacterOwnedBySomeoneElse_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-effect", "player-a")

	conn := dialAndJoin(t, ts, "campaign-effect", "player-b")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestApplyEffect(ctx, conn, "player-b", "campaign-effect", "char-1", map[string]any{"effectType": "heal", "amount": 5}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (player-b does not own char-1)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_CharacterApplyEffect_NoSystemEngineConfigured_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t) // no system engine, no character store
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestApplyEffect(ctx, conn, "player-a", "campaign-1", "char-1", map[string]any{"effectType": "heal", "amount": 5}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}
