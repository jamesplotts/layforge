// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// startCombatForEnforcementTest drives a real start_combat DM tool call
// (character_ids ["char-a", "char-b"], char-a rigged to roll higher so
// it leads initiative) via starterConn, and returns once the resulting
// turn.state confirms combat is active with char-a current — the
// precondition every test in this file needs before exercising
// enforceTurnOrder from a *different*, freshly-dialed connection (so a
// test's own read of the player-action response is never confused with
// this setup call's own trailing tool.result/narrative.dm_prose
// broadcasts still sitting unread on starterConn).
func startCombatForEnforcementTest(ctx context.Context, t *testing.T, starterConn *websocket.Conn, campaignID string) {
	t.Helper()
	if _, err := sendPlayerInput(ctx, starterConn, campaignID, "player-a", "char-a", "We're ambushed!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, starterConn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	state := readTurnState(ctx, t, starterConn)
	if !state.Active {
		t.Fatalf("turn.state Active = false, want true")
	}
	if state.CurrentCharacterID != "char-a" {
		t.Fatalf("turn.state CurrentCharacterID = %q, want %q (char-a rolled higher initiative)", state.CurrentCharacterID, "char-a")
	}
}

func newCombatFakeEngine() *fakeSystemEngineClient {
	return &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			total := int32(8)
			if req.Actor.ActorId == "char-a" {
				total = 19 // char-a leads initiative
			}
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{Total: total, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: total, Label: "d20"}}},
			}, nil
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
	}
}

func newCombatFakeLLM() *fakeLLMProvider {
	return &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The fight begins."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`),
			}}},
			{Text: "Roll for initiative!"},
		},
	}
}

func TestServe_RollCheckRequest_CombatActive_OutOfTurn_Rejected(t *testing.T) {
	fakeEngine := newCombatFakeEngine()
	ts, st := newTestServerWithLLMAndSystemEngine(t, newCombatFakeLLM(), fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn-enforce-1", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn-enforce-1", "player-b")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	starter := dialAndJoin(t, ts, "campaign-turn-enforce-1", "player-a")
	defer starter.CloseNow()
	startCombatForEnforcementTest(ctx, t, starter, "campaign-turn-enforce-1")

	// char-b is not the current character — a fresh connection so this
	// read can't be confused with the starter connection's own trailing
	// slow-pass broadcasts.
	b := dialAndJoin(t, ts, "campaign-turn-enforce-1", "player-b")
	defer b.CloseNow()
	// start_combat's own initiative rolls already called ResolveCheck for
	// both characters — capture the count now so the assertion below
	// checks whether char-b's *out-of-turn* roll added a new call, not
	// whether ResolveCheck was ever called for char-b at all.
	resolveCallsBefore := len(fakeEngine.resolveCheckRequests)
	if err := requestRollCheck(ctx, b, "campaign-turn-enforce-1", "player-b", "char-b", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}
	typ, data, err := readEnvelopeType(ctx, b)
	if err != nil {
		t.Fatalf("reading response to char-b's out-of-turn roll: %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (out-of-turn roll should be rejected)", typ, protocol.MessageTypeSystemError)
	}
	var errMsg protocol.SystemErrorMessage
	if err := json.Unmarshal(data, &errMsg); err != nil {
		t.Fatalf("unmarshaling system.error: %v", err)
	}
	if !strings.Contains(errMsg.Payload.Message, "not your turn") {
		t.Errorf("system.error Message = %q, want it to mention whose turn it is", errMsg.Payload.Message)
	}
	if len(fakeEngine.resolveCheckRequests) != resolveCallsBefore {
		t.Error("ResolveCheck was called for char-b's out-of-turn roll, want the gate to have blocked it before reaching the system engine")
	}
}

func TestServe_RollCheckRequest_CombatActive_YourTurn_Succeeds(t *testing.T) {
	fakeEngine := newCombatFakeEngine()
	ts, st := newTestServerWithLLMAndSystemEngine(t, newCombatFakeLLM(), fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn-enforce-2", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn-enforce-2", "player-b")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	starter := dialAndJoin(t, ts, "campaign-turn-enforce-2", "player-a")
	defer starter.CloseNow()
	startCombatForEnforcementTest(ctx, t, starter, "campaign-turn-enforce-2")

	a := dialAndJoin(t, ts, "campaign-turn-enforce-2", "player-a")
	defer a.CloseNow()
	if err := requestRollCheck(ctx, a, "campaign-turn-enforce-2", "player-a", "char-a", "ability_check", "Strength"); err != nil {
		t.Fatalf("requestRollCheck() error = %v", err)
	}
	typ, _, err := readEnvelopeType(ctx, a)
	if err != nil {
		t.Fatalf("reading response to char-a's on-turn roll: %v", err)
	}
	if typ != protocol.MessageTypeRollRequest {
		t.Errorf("response type = %q, want %q (it is char-a's turn)", typ, protocol.MessageTypeRollRequest)
	}
}

func TestServe_CharacterApplyEffect_CombatActive_OutOfTurn_Rejected(t *testing.T) {
	fakeEngine := newCombatFakeEngine()
	ts, st := newTestServerWithLLMAndSystemEngine(t, newCombatFakeLLM(), fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn-enforce-3", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn-enforce-3", "player-b")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	starter := dialAndJoin(t, ts, "campaign-turn-enforce-3", "player-a")
	defer starter.CloseNow()
	startCombatForEnforcementTest(ctx, t, starter, "campaign-turn-enforce-3")

	b := dialAndJoin(t, ts, "campaign-turn-enforce-3", "player-b")
	defer b.CloseNow()
	if err := requestApplyEffect(ctx, b, "player-b", "campaign-turn-enforce-3", "char-b", map[string]any{"effectType": "heal", "amount": 5}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}
	typ, data, err := readEnvelopeType(ctx, b)
	if err != nil {
		t.Fatalf("reading response to char-b's out-of-turn effect: %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (out-of-turn effect should be rejected)", typ, protocol.MessageTypeSystemError)
	}
	var errMsg protocol.SystemErrorMessage
	if err := json.Unmarshal(data, &errMsg); err != nil {
		t.Fatalf("unmarshaling system.error: %v", err)
	}
	if !strings.Contains(errMsg.Payload.Message, "not your turn") {
		t.Errorf("system.error Message = %q, want it to mention whose turn it is", errMsg.Payload.Message)
	}
	if fakeEngine.lastApplyEffectRequest != nil {
		t.Error("ApplyEffect was called for char-b's out-of-turn effect, want the gate to have blocked it before reaching the system engine")
	}
}

func TestServe_CharacterApplyEffect_CombatActive_YourTurn_Succeeds(t *testing.T) {
	updatedData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "hitPoints": map[string]any{"current": 15.0, "max": 20.0}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := newCombatFakeEngine()
	fakeEngine.applyEffectResp = &systemenginepb.ApplyEffectResponse{
		Success: true,
		Actor:   &systemenginepb.Actor{ActorId: "char-a", CharacterData: updatedData, SchemaVersion: "opencombatengine-v1"},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, newCombatFakeLLM(), fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn-enforce-4", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn-enforce-4", "player-b")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	starter := dialAndJoin(t, ts, "campaign-turn-enforce-4", "player-a")
	defer starter.CloseNow()
	startCombatForEnforcementTest(ctx, t, starter, "campaign-turn-enforce-4")

	a := dialAndJoin(t, ts, "campaign-turn-enforce-4", "player-a")
	defer a.CloseNow()
	if err := requestApplyEffect(ctx, a, "player-a", "campaign-turn-enforce-4", "char-a", map[string]any{"effectType": "heal", "amount": 5}); err != nil {
		t.Fatalf("requestApplyEffect() error = %v", err)
	}
	typ, _, err := readEnvelopeType(ctx, a)
	if err != nil {
		t.Fatalf("reading response to char-a's on-turn effect: %v", err)
	}
	if typ != protocol.MessageTypeCharacterState {
		t.Errorf("response type = %q, want %q (it is char-a's turn)", typ, protocol.MessageTypeCharacterState)
	}
}
