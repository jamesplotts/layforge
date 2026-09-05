// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// seedCharacter directly saves a character into st, bypassing
// character.upload dispatch — these tests are about roll.check_request,
// not import, so a real store write is enough setup. Status is
// Approved: these tests exercise roll mechanics/ownership/turn-order,
// not the character-import review flow (character_review_test.go), so
// they need a character ownedCharacter's requireApproved gate won't
// reject.
func seedCharacter(t *testing.T, st *store.SQLiteEventStore, id, campaignID, ownerID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.SaveCharacter(context.Background(), store.Character{
		ID:            id,
		CampaignID:    campaignID,
		OwnerID:       ownerID,
		SchemaVersion: "opencombatengine-v1",
		Status:        store.CharacterStatusApproved,
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
}

// requestRollCheck sends a roll.check_request and reads back one message
// as raw bytes — callers decode it themselves, since a successful request
// produces two broadcasts (roll.request then roll.result) while a
// rejected one produces a single system.error.
func requestRollCheck(ctx context.Context, conn *websocket.Conn, campaignID, sender, characterID, checkType, ability string) error {
	msg := protocol.RollCheckRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "roll-req-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeRollCheckRequest,
		},
		Payload: protocol.RollCheckRequestPayload{CharacterID: characterID, CheckType: checkType, Ability: ability},
	}
	return wsjson.Write(ctx, conn, msg)
}

func readEnvelopeType(ctx context.Context, conn *websocket.Conn) (protocol.MessageType, []byte, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return "", nil, err
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", nil, fmt.Errorf("unmarshaling envelope: %w", err)
	}
	return envelope.Type, data, nil
}

func TestServe_RollCheckRequest_OwnedCharacter_BroadcastsRollRequestThenRollResult(t *testing.T) {
	resolveResp := &systemenginepb.ResolveCheckResponse{
		Success: true,
		Outcome: &systemenginepb.Outcome{
			Total:         14,
			ResultSummary: "resolved",
			Rolls:         []*systemenginepb.DieRoll{{Sides: 20, Result: 11, Label: "d20"}},
		},
	}
	fake := &fakeSystemEngineClient{resolveCheckResp: resolveResp}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll", "player-a")

	a := dialAndJoin(t, ts, "campaign-roll", "player-a")
	defer a.CloseNow()
	b := dialAndJoin(t, ts, "campaign-roll", "player-b")
	defer b.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, a, "campaign-roll", "player-a", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	for name, conn := range map[string]*websocket.Conn{"player-a (requester)": a, "player-b (bystander)": b} {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("%s: reading roll.request error = %v", name, err)
		}
		if typ != protocol.MessageTypeRollRequest {
			t.Fatalf("%s: first broadcast type = %q, want %q", name, typ, protocol.MessageTypeRollRequest)
		}
		var reqMsg protocol.RollRequestMessage
		if err := json.Unmarshal(data, &reqMsg); err != nil {
			t.Fatalf("%s: unmarshaling roll.request error = %v", name, err)
		}
		if reqMsg.Payload.CharacterID != "char-1" {
			t.Errorf("%s: roll.request CharacterID = %q, want %q", name, reqMsg.Payload.CharacterID, "char-1")
		}
		if len(reqMsg.Payload.RollSpec.Dice) != 1 || reqMsg.Payload.RollSpec.Dice[0].Sides != 20 || reqMsg.Payload.RollSpec.Dice[0].Count != 1 {
			t.Errorf("%s: roll.request RollSpec.Dice = %+v, want one {Sides:20 Count:1}", name, reqMsg.Payload.RollSpec.Dice)
		}

		typ, data, err = readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("%s: reading roll.result error = %v", name, err)
		}
		if typ != protocol.MessageTypeRollResult {
			t.Fatalf("%s: second broadcast type = %q, want %q", name, typ, protocol.MessageTypeRollResult)
		}
		var resultMsg protocol.RollResultMessage
		if err := json.Unmarshal(data, &resultMsg); err != nil {
			t.Fatalf("%s: unmarshaling roll.result error = %v", name, err)
		}
		if resultMsg.Payload.Total != 14 {
			t.Errorf("%s: roll.result Total = %d, want 14", name, resultMsg.Payload.Total)
		}
		if len(resultMsg.Payload.Rolls) != 1 || resultMsg.Payload.Rolls[0].Result != 11 {
			t.Errorf("%s: roll.result Rolls = %+v, want one DieRoll with Result 11", name, resultMsg.Payload.Rolls)
		}
	}

	if fake.lastResolveCheckRequest.Actor.ActorId != "char-1" {
		t.Errorf("ResolveCheck called with Actor.ActorId = %q, want %q", fake.lastResolveCheckRequest.Actor.ActorId, "char-1")
	}
	if fake.lastResolveCheckRequest.Params.Fields["checkType"].GetStringValue() != "ability_check" {
		t.Errorf("ResolveCheck called with params.checkType = %v, want %q", fake.lastResolveCheckRequest.Params.Fields["checkType"], "ability_check")
	}
	if fake.lastResolveCheckRequest.Params.Fields["ability"].GetStringValue() != "Strength" {
		t.Errorf("ResolveCheck called with params.ability = %v, want %q", fake.lastResolveCheckRequest.Params.Fields["ability"], "Strength")
	}
}

func TestServe_RollCheckRequest_CharacterOwnedBySomeoneElse_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll", "player-a")

	conn := dialAndJoin(t, ts, "campaign-roll", "player-b")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-roll", "player-b", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (player-b does not own char-1)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_RollCheckRequest_UnknownCharacter_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, _ := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-roll", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-roll", "player-a", "does-not-exist", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_RollCheckRequest_NoSystemEngineConfigured_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t) // no system engine, no character store
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-1", "player-a", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_RollCheckRequest_EngineReportsFailure_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{Success: false, Error: "missing ability"},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll", "player-a")

	conn := dialAndJoin(t, ts, "campaign-roll", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, conn, "campaign-roll", "player-a", "char-1", "ability_check", ""); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_RollCheckRequest_EngineCallFails_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	fake := &fakeSystemEngineClient{resolveCheckErr: errors.New("sidecar unreachable")}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll", "player-a")

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
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}

	// Connection must still be usable afterward.
	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-after-roll-error",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-roll",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) after roll error error = %v", err)
	}
	var gotBroadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &gotBroadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) after roll error error = %v", err)
	}
}
