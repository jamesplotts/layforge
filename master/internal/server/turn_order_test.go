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

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// readTurnState reads messages from conn until a turn.state arrives (or
// 20 messages pass), skipping over the roll.request/roll.result/
// tool.result traffic a DM combat tool call also produces — the same
// "read generically by type" approach dm_slow_pass_test.go uses, since
// only "a turn.state eventually arrives" is the actual contract, not a
// strict message ordering.
func readTurnState(ctx context.Context, t *testing.T, conn *websocket.Conn) protocol.TurnStatePayload {
	t.Helper()
	for i := 0; i < 20; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ != protocol.MessageTypeTurnState {
			continue
		}
		var msg protocol.TurnStateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshaling turn.state: %v", err)
		}
		return msg.Payload
	}
	t.Fatal("no turn.state message arrived within 20 messages")
	return protocol.TurnStatePayload{}
}

func TestServe_NarrativePlayerInput_SlowPass_StartCombat_RollsInitiativeAndBroadcastsOrder(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			total := int32(8)
			if req.Actor.ActorId == "char-b" {
				total = 19
			}
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{
					Total: total, ResultSummary: "resolved",
					Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: total, Label: "d20"}},
				},
			}, nil
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`),
			}}}, // slow pass: starts combat
			{Text: "The goblins spring the ambush — roll for initiative!"}, // final narration
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn", "player-a")

	conn := dialAndJoin(t, ts, "campaign-turn", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-turn", "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	state := readTurnState(ctx, t, conn)
	if !state.Active {
		t.Fatal("turn.state Active = false, want true")
	}
	if len(state.Order) != 2 || state.Order[0] != "char-b" || state.Order[1] != "char-a" {
		t.Errorf("turn.state Order = %v, want [char-b char-a] (char-b rolled higher)", state.Order)
	}
	if state.CurrentCharacterID != "char-b" {
		t.Errorf("turn.state CurrentCharacterID = %q, want %q", state.CurrentCharacterID, "char-b")
	}
	if state.Round != 1 {
		t.Errorf("turn.state Round = %d, want 1", state.Round)
	}

	if len(fakeEngine.resolveCheckRequests) != 2 {
		t.Fatalf("ResolveCheck called %d times, want 2 (one initiative roll per character)", len(fakeEngine.resolveCheckRequests))
	}
	for _, req := range fakeEngine.resolveCheckRequests {
		if req.Params.Fields["ability"].GetStringValue() != "Dexterity" {
			t.Errorf("initiative roll for %q used ability %q, want Dexterity", req.Actor.ActorId, req.Params.Fields["ability"].GetStringValue())
		}
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_SkipsUnconsciousCharacter(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			totals := map[string]int32{"char-a": 20, "char-b": 15, "char-c": 10}
			total := totals[req.Actor.ActorId]
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{
					Total: total, ResultSummary: "resolved",
					Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: total, Label: "d20"}},
				},
			}, nil
		},
		getCharacterStatusFunc: func(req *systemenginepb.GetCharacterStatusRequest) (*systemenginepb.GetCharacterStatusResponse, error) {
			// char-b is already unconscious throughout — both start_combat
			// (which must exclude it from initiative) and advance_turn
			// (which must never land a turn on it) see the same status.
			if req.Actor.ActorId == "char-b" {
				return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_UNCONSCIOUS}, nil
			}
			return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}, nil
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party braces for battle."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-b","char-c"]}`),
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_2", Name: "advance_turn", Arguments: json.RawMessage(`{}`),
			}}},
			{Text: "Kestrel's turn ends; the goblin, still unconscious, is passed over."}, // final narration
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn2", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn2", "player-a")
	seedCharacter(t, st, "char-c", "campaign-turn2", "player-a")

	conn := dialAndJoin(t, ts, "campaign-turn2", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-turn2", "player-a", "char-a", "Fight!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	// First turn.state is start_combat's (char-a leads, order [a b c]);
	// the second is advance_turn's, which must have skipped char-b.
	_ = readTurnState(ctx, t, conn)
	state := readTurnState(ctx, t, conn)
	if !state.Active {
		t.Fatal("turn.state Active = false after advance_turn, want true (char-c is still active)")
	}
	if state.CurrentCharacterID != "char-c" {
		t.Errorf("turn.state CurrentCharacterID = %q, want %q (char-b must be skipped, unconscious)", state.CurrentCharacterID, "char-c")
	}
	if state.Round != 1 {
		t.Errorf("turn.state Round = %d, want 1 (no wraparound yet)", state.Round)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_AllIncapacitated_EndsCombatAutomatically(t *testing.T) {
	statusCalls := 0
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 10, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 10, Label: "d20"}}},
		},
		getCharacterStatusFunc: func(*systemenginepb.GetCharacterStatusRequest) (*systemenginepb.GetCharacterStatusResponse, error) {
			statusCalls++
			// The first two status checks are start_combat's (both
			// characters must be active to enter initiative); every check
			// from then on — all inside advance_turn — reports
			// unconscious, simulating both combatants going down over the
			// course of the fight before advance_turn is next called.
			if statusCalls <= 2 {
				return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}, nil
			}
			return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_UNCONSCIOUS}, nil
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The last two combatants stagger."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`),
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_2", Name: "advance_turn", Arguments: json.RawMessage(`{}`),
			}}},
			{Text: "Both combatants collapse — the fight is over."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn3", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn3", "player-a")

	conn := dialAndJoin(t, ts, "campaign-turn3", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-turn3", "player-a", "char-a", "We trade final blows."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	_ = readTurnState(ctx, t, conn) // start_combat's turn.state
	state := readTurnState(ctx, t, conn)
	if state.Active {
		t.Errorf("turn.state Active = true after advance_turn found no active characters, want false (combat should auto-end)")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_EndCombat_BroadcastsInactiveTurnState(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 12, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 12, Label: "d20"}}},
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The bandit throws down his weapon."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a"]}`),
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_2", Name: "end_combat", Arguments: json.RawMessage(`{}`),
			}}},
			{Text: "The bandit surrenders; the fight ends before it truly begins."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn4", "player-a")

	conn := dialAndJoin(t, ts, "campaign-turn4", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-turn4", "player-a", "char-a", "I raise my sword."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	_ = readTurnState(ctx, t, conn) // start_combat's turn.state
	state := readTurnState(ctx, t, conn)
	if state.Active {
		t.Error("turn.state Active = true after end_combat, want false")
	}
	if len(state.Order) != 0 || state.CurrentCharacterID != "" {
		t.Errorf("turn.state after end_combat = %+v, want a bare inactive payload", state)
	}
}
