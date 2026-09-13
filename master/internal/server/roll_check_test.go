// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
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

// requestRollCheck sends a roll.check_request. A successful request now
// asynchronously (see resolveCheck's own doc comment on why it must not
// block its own connection's read loop) produces client.roll (to the
// requester) and client.roll_spectate (to everyone else); a rejected one
// produces a single, synchronous system.error.
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

// sendClientRollReveal sends a client.roll_reveal for one die, as the
// roller (sender must match the roll's own character.OwnerID).
func sendClientRollReveal(ctx context.Context, conn *websocket.Conn, campaignID, sender, promptID, dieID string) error {
	msg := protocol.ClientRollRevealMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "reveal-" + dieID,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeClientRollReveal,
		},
		Payload: protocol.ClientRollRevealPayload{PromptID: promptID, DieID: dieID},
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

func TestServe_RollCheckRequest_OwnedCharacter_SendsClientRollThenSpectateThenComplete(t *testing.T) {
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

	// resolveCheck sends the wait off on its own goroutine (it must not
	// block its own connection's read loop, since that's exactly what
	// needs to stay free to read player-a's own reveal below) — read
	// player-a's client.roll first, since nothing else is expected on
	// that connection before it.
	typ, data, err := readEnvelopeType(ctx, a)
	if err != nil {
		t.Fatalf("player-a: reading client.roll error = %v", err)
	}
	if typ != protocol.MessageTypeClientRoll {
		t.Fatalf("player-a: first message type = %q, want %q", typ, protocol.MessageTypeClientRoll)
	}
	var rollMsg protocol.ClientRollMessage
	if err := json.Unmarshal(data, &rollMsg); err != nil {
		t.Fatalf("player-a: unmarshaling client.roll error = %v", err)
	}
	if rollMsg.Payload.CharacterID != "char-1" {
		t.Errorf("client.roll CharacterID = %q, want %q", rollMsg.Payload.CharacterID, "char-1")
	}
	if len(rollMsg.Payload.Dice) != 1 || rollMsg.Payload.Dice[0].Sides != 20 || rollMsg.Payload.Dice[0].Result != 11 {
		t.Fatalf("client.roll Dice = %+v, want one {Sides:20 Result:11}", rollMsg.Payload.Dice)
	}
	promptID, dieID := rollMsg.Payload.PromptID, rollMsg.Payload.Dice[0].ID

	// player-b (a bystander, never touching this character) must see the
	// spectator twin first — same dice, no result at all (anti-
	// metagaming, design doc §9.7) — decoded into a raw map so a zero
	// Result can't be confused with "the field was simply never set."
	typ, data, err = readEnvelopeType(ctx, b)
	if err != nil {
		t.Fatalf("player-b: reading client.roll_spectate error = %v", err)
	}
	if typ != protocol.MessageTypeClientRollSpectate {
		t.Fatalf("player-b: first broadcast type = %q, want %q", typ, protocol.MessageTypeClientRollSpectate)
	}
	var rawSpectate map[string]any
	if err := json.Unmarshal(data, &rawSpectate); err != nil {
		t.Fatalf("player-b: unmarshaling client.roll_spectate error = %v", err)
	}
	spectateDice, _ := rawSpectate["payload"].(map[string]any)["dice"].([]any)
	if len(spectateDice) != 1 {
		t.Fatalf("client.roll_spectate dice = %+v, want exactly 1", spectateDice)
	}
	if _, hasResult := spectateDice[0].(map[string]any)["result"]; hasResult {
		t.Errorf("client.roll_spectate die = %+v, want no 'result' key at all", spectateDice[0])
	}

	// player-a reveals the one die — this is the actual player action
	// under test.
	if err := sendClientRollReveal(ctx, a, "campaign-roll", "player-a", promptID, dieID); err != nil {
		t.Fatalf("sendClientRollReveal() error = %v", err)
	}

	// player-b sees the real result fill in, then both connections see
	// client.roll_complete with the real total.
	typ, data, err = readEnvelopeType(ctx, b)
	if err != nil {
		t.Fatalf("player-b: reading client.roll_spectate_reveal error = %v", err)
	}
	if typ != protocol.MessageTypeClientRollSpectateReveal {
		t.Fatalf("player-b: second broadcast type = %q, want %q", typ, protocol.MessageTypeClientRollSpectateReveal)
	}
	var revealMsg protocol.ClientRollSpectateRevealMessage
	if err := json.Unmarshal(data, &revealMsg); err != nil {
		t.Fatalf("player-b: unmarshaling client.roll_spectate_reveal error = %v", err)
	}
	if revealMsg.Payload.DieID != dieID || revealMsg.Payload.Result != 11 {
		t.Errorf("client.roll_spectate_reveal = %+v, want {DieID:%q Result:11}", revealMsg.Payload, dieID)
	}

	for name, conn := range map[string]*websocket.Conn{"player-a": a, "player-b": b} {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("%s: reading client.roll_complete error = %v", name, err)
		}
		if typ != protocol.MessageTypeClientRollComplete {
			t.Fatalf("%s: message type = %q, want %q", name, typ, protocol.MessageTypeClientRollComplete)
		}
		var completeMsg protocol.ClientRollCompleteMessage
		if err := json.Unmarshal(data, &completeMsg); err != nil {
			t.Fatalf("%s: unmarshaling client.roll_complete error = %v", name, err)
		}
		if completeMsg.Payload.Total != 14 {
			t.Errorf("%s: client.roll_complete Total = %d, want 14", name, completeMsg.Payload.Total)
		}
		if !strings.Contains(completeMsg.Payload.ResultSummary, "Strength Check") {
			t.Errorf("%s: client.roll_complete ResultSummary = %q, want it to mention %q", name, completeMsg.Payload.ResultSummary, "Strength Check")
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

func TestServe_ClientRollReveal_WrongSender_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 14, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 11, Label: "d20"}}},
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll-reveal-wrong", "player-a")

	a := dialAndJoin(t, ts, "campaign-roll-reveal-wrong", "player-a")
	defer a.CloseNow()
	b := dialAndJoin(t, ts, "campaign-roll-reveal-wrong", "player-b")
	defer b.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestRollCheck(ctx, a, "campaign-roll-reveal-wrong", "player-a", "char-1", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}
	typ, data, err := readEnvelopeType(ctx, a)
	if err != nil {
		t.Fatalf("player-a: reading client.roll error = %v", err)
	}
	if typ != protocol.MessageTypeClientRoll {
		t.Fatalf("player-a: message type = %q, want %q", typ, protocol.MessageTypeClientRoll)
	}
	var rollMsg protocol.ClientRollMessage
	if err := json.Unmarshal(data, &rollMsg); err != nil {
		t.Fatalf("unmarshaling client.roll error = %v", err)
	}
	promptID, dieID := rollMsg.Payload.PromptID, rollMsg.Payload.Dice[0].ID

	// Drain player-b's own client.roll_spectate first — it arrives
	// unprompted as soon as the roll is sent, before the reveal attempt
	// below, and isn't what this test is checking.
	if typ, _, err := readEnvelopeType(ctx, b); err != nil {
		t.Fatalf("player-b: reading client.roll_spectate error = %v", err)
	} else if typ != protocol.MessageTypeClientRollSpectate {
		t.Fatalf("player-b: message type = %q, want %q", typ, protocol.MessageTypeClientRollSpectate)
	}

	// player-b never owned char-1 — attempting to reveal it anyway must
	// be rejected.
	if err := sendClientRollReveal(ctx, b, "campaign-roll-reveal-wrong", "player-b", promptID, dieID); err != nil {
		t.Fatalf("sendClientRollReveal() error = %v", err)
	}
	typ, _, err = readEnvelopeType(ctx, b)
	if err != nil {
		t.Fatalf("player-b: reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("player-b: response type = %q, want %q (not their roll to reveal)", typ, protocol.MessageTypeSystemError)
	}

	// The roll must still be intact for its real owner afterward — a
	// rejected reveal attempt must never corrupt or consume the registry
	// entry.
	if err := sendClientRollReveal(ctx, a, "campaign-roll-reveal-wrong", "player-a", promptID, dieID); err != nil {
		t.Fatalf("sendClientRollReveal() (real owner) error = %v", err)
	}
	typ, _, err = readEnvelopeType(ctx, a)
	if err != nil {
		t.Fatalf("player-a: reading client.roll_complete error = %v", err)
	}
	if typ != protocol.MessageTypeClientRollComplete {
		t.Fatalf("player-a: message type = %q, want %q (the real owner's reveal should still work)", typ, protocol.MessageTypeClientRollComplete)
	}
}

func TestServe_ClientRollReveal_UnknownPromptID_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-roll-reveal-unknown", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendClientRollReveal(ctx, conn, "campaign-roll-reveal-unknown", "player-a", "no-such-prompt", "no-such-die"); err != nil {
		t.Fatalf("sendClientRollReveal() error = %v", err)
	}
	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_ClientRollReveal_Timeout_SelfReveals_AndCompletesMechanicsPass(t *testing.T) {
	origTimeout := server.ClientRollTimeout
	server.ClientRollTimeout = 200 * time.Millisecond
	t.Cleanup(func() { server.ClientRollTimeout = origTimeout })

	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 12, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 9, Label: "d20"}}},
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel tries to sneak past the guard."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "resolve_check",
				Arguments: json.RawMessage(`{"character_id":"char-1","check_type":"ability_check","ability":"Dexterity","skill":"Stealth"}`),
			}}}, // mechanics pass
			{Text: "Kestrel slips past unseen."}, // narration pass
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-roll-timeout", "player-a")

	conn := dialAndJoin(t, ts, "campaign-roll-timeout", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-roll-timeout", "player-a", "char-1", "I sneak past the guard."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	// Never sends client.roll_reveal — the whole point of this test is
	// the self-reveal timeout path. client.roll_complete must still
	// arrive (with the real, already-known total) and the mechanics/
	// narration passes must still complete, rather than hanging or
	// silently failing the turn.
	var sawComplete bool
	for i := 0; i < 10; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d error = %v", i, err)
		}
		if typ == protocol.MessageTypeClientRollComplete {
			sawComplete = true
			var rc protocol.ClientRollCompleteMessage
			if err := json.Unmarshal(data, &rc); err != nil {
				t.Fatalf("unmarshaling client.roll_complete error = %v", err)
			}
			if rc.Payload.Total != 12 {
				t.Errorf("client.roll_complete Total = %d, want 12", rc.Payload.Total)
			}
		}
		if typ == protocol.MessageTypeClientDisplay {
			break
		}
	}
	if !sawComplete {
		t.Error("no client.roll_complete arrived via the self-reveal timeout path")
	}
}
