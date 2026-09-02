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

// noOpStartTurnResp is what most tests in this file want for StartTurn:
// bookkeeping ran, nothing notable happened (no death save, no updated
// Actor to persist). Tests that care about the automatic death save use
// startTurnFunc instead.
var noOpStartTurnResp = &systemenginepb.StartTurnResponse{Success: true}

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
		startTurnResp:          noOpStartTurnResp,
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

	// start_combat also starts order[0] (char-b)'s own turn.
	if len(fakeEngine.startTurnRequests) != 1 || fakeEngine.startTurnRequests[0].Actor.ActorId != "char-b" {
		t.Errorf("startTurnRequests = %+v, want exactly one call for char-b", fakeEngine.startTurnRequests)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_SkipsDeadCharacter
// covers characterIsDead's actual skip semantics: a character
// get_character_status reports dead is the one status advanceTurn
// removes from rotation entirely — never landed on, never given
// StartTurn. Unconscious/dying characters are NOT skipped (see
// TestServe_..._AdvanceTurn_UnconsciousCharacter_RollsAutomaticDeathSave
// below) — only dead ones are.
func TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_SkipsDeadCharacter(t *testing.T) {
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
			// char-b is dead throughout — both start_combat (which must
			// exclude it from initiative) and advance_turn (which must
			// never land a turn on it) see the same status.
			if req.Actor.ActorId == "char-b" {
				return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD}, nil
			}
			return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}, nil
		},
		startTurnResp: noOpStartTurnResp,
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party braces for battle."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-c"]}`),
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_2", Name: "advance_turn", Arguments: json.RawMessage(`{}`),
			}}},
			{Text: "Kestrel's turn ends; the fight moves to the next combatant."}, // final narration
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

	// First turn.state is start_combat's (char-a leads, order [a c] —
	// char-b was already dead, excluded from initiative entirely); the
	// second is advance_turn's.
	_ = readTurnState(ctx, t, conn)
	state := readTurnState(ctx, t, conn)
	if !state.Active {
		t.Fatal("turn.state Active = false after advance_turn, want true (char-c is still active)")
	}
	if state.CurrentCharacterID != "char-c" {
		t.Errorf("turn.state CurrentCharacterID = %q, want %q", state.CurrentCharacterID, "char-c")
	}
	if state.Round != 1 {
		t.Errorf("turn.state Round = %d, want 1 (no wraparound yet)", state.Round)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_UnconsciousCharacter_RollsAutomaticDeathSave
// covers the actual SRD rule design doc §9.3 is really asking for: an
// unconscious/dying character still gets a turn — they roll a death
// saving throw instead of acting — rather than being skipped over.
// Skipping them outright (an earlier version of this codebase's
// behavior) would leave them stuck at 0 HP forever, never progressing
// toward stabilizing or dying, since they'd never get StartTurn called
// again. Here char-b is unconscious and rolls higher initiative than
// char-c, so advance_turn from char-a must land ON char-b — not skip to
// char-c — and startTurnFor's automatic death save must broadcast as a
// real roll.result.
func TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_UnconsciousCharacter_RollsAutomaticDeathSave(t *testing.T) {
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
			if req.Actor.ActorId == "char-b" {
				return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_UNCONSCIOUS}, nil
			}
			return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}, nil
		},
		startTurnFunc: func(req *systemenginepb.StartTurnRequest) (*systemenginepb.StartTurnResponse, error) {
			if req.Actor.ActorId != "char-b" {
				return noOpStartTurnResp, nil
			}
			return &systemenginepb.StartTurnResponse{
				Success:         true,
				DeathSaveRolled: true,
				DeathSaveOutcome: &systemenginepb.Outcome{
					Total: 13, ResultSummary: "success",
					Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 13, Label: "d20"}},
				},
			}, nil
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party braces for battle."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-a","char-b","char-c"]}`),
			}}},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_2", Name: "advance_turn", Arguments: json.RawMessage(`{}`),
			}}},
			{Text: "Kestrel's turn ends; the fallen goblin fights for its life."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-turn2b", "player-a")
	seedCharacter(t, st, "char-b", "campaign-turn2b", "player-a")
	seedCharacter(t, st, "char-c", "campaign-turn2b", "player-a")

	conn := dialAndJoin(t, ts, "campaign-turn2b", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-turn2b", "player-a", "char-a", "Fight!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	_ = readTurnState(ctx, t, conn) // start_combat's turn.state (order [a b c])

	// advance_turn's own effects arrive next, in order: the automatic
	// death save's roll.request/roll.result (startTurnFor runs and
	// broadcasts before advanceTurn broadcasts turn.state), then
	// turn.state itself. Read generically by type — like
	// dm_slow_pass_test.go's tool-call test — rather than assuming exact
	// ordering beyond "the death save broadcasts before turn.state",
	// which the code path guarantees.
	var sawDeathSaveResult bool
	var state protocol.TurnStatePayload
	for i := 0; i < 20; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		switch typ {
		case protocol.MessageTypeRollResult:
			var rr protocol.RollResultMessage
			if err := json.Unmarshal(data, &rr); err != nil {
				t.Fatalf("unmarshaling roll.result: %v", err)
			}
			if rr.Payload.CharacterID == "char-b" {
				sawDeathSaveResult = true
				if rr.Payload.Total != 13 {
					t.Errorf("death save roll.result Total = %d, want 13", rr.Payload.Total)
				}
			}
		case protocol.MessageTypeTurnState:
			var msg protocol.TurnStateMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatalf("unmarshaling turn.state: %v", err)
			}
			state = msg.Payload
		}
		if state.CurrentCharacterID != "" {
			break
		}
	}

	if !state.Active {
		t.Fatal("turn.state Active = false after advance_turn, want true")
	}
	if state.CurrentCharacterID != "char-b" {
		t.Errorf("turn.state CurrentCharacterID = %q, want %q (unconscious characters get a turn, they aren't skipped)", state.CurrentCharacterID, "char-b")
	}
	if !sawDeathSaveResult {
		t.Error("no roll.result for char-b's automatic death save arrived, want startTurnFor to broadcast it like any other roll")
	}

	found := false
	for _, req := range fakeEngine.startTurnRequests {
		if req.Actor.ActorId == "char-b" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("StartTurn was never called for char-b, want it called when advance_turn lands on an unconscious character")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AdvanceTurn_AllDead_EndsCombatAutomatically(t *testing.T) {
	statusCalls := 0
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 10, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 10, Label: "d20"}}},
		},
		startTurnResp: noOpStartTurnResp,
		getCharacterStatusFunc: func(*systemenginepb.GetCharacterStatusRequest) (*systemenginepb.GetCharacterStatusResponse, error) {
			statusCalls++
			// The first two status checks are start_combat's (both
			// characters must be non-dead to enter initiative); every
			// check from then on — all inside advance_turn — reports
			// dead, simulating both combatants dying over the course of
			// the fight before advance_turn is next called.
			if statusCalls <= 2 {
				return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}, nil
			}
			return &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD}, nil
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
		t.Errorf("turn.state Active = true after advance_turn found no non-dead characters, want false (combat should auto-end)")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_EndCombat_BroadcastsInactiveTurnState(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 12, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 12, Label: "d20"}}},
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          noOpStartTurnResp,
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
