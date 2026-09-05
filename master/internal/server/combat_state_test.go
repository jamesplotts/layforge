// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// combatStatePayload mirrors the JSON shape package server's
// combatStateSnapshot writes (its own Go types are unexported, so tests
// outside that package check the wire shape instead — which is really
// what matters here, the same "verify what actually round-trips"
// reasoning as any other persistence test in this codebase).
type combatStatePayload struct {
	TurnOrder *struct {
		Active       bool     `json:"active"`
		Order        []string `json:"order"`
		CurrentIndex int      `json:"current_index"`
		Round        int      `json:"round"`
	} `json:"turn_order"`
	CombatMap *struct {
		State struct {
			Tokens []struct {
				CharacterID string `json:"CharacterID"`
			} `json:"Tokens"`
		} `json:"state"`
	} `json:"combat_map"`
}

// startCombatFakeEngine builds a fakeSystemEngineClient sufficient to
// drive start_combat/advance_turn/generate_combat_map/end_combat for two
// characters, char-a and char-b, with char-b always winning initiative —
// the same shape turn_order_test.go's own StartCombat test already uses.
func startCombatFakeEngine() *fakeSystemEngineClient {
	return &fakeSystemEngineClient{
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
}

func TestCombatState_StartCombatAndAdvanceTurn_PersistsSnapshot(t *testing.T) {
	fakeEngine := startCombatFakeEngine()
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{Text: "Roll for initiative!"},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-persist", "player-a")
	seedCharacter(t, st, "char-b", "campaign-persist", "player-a")

	conn := dialAndJoin(t, ts, "campaign-persist", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-persist", "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	readTurnState(ctx, t, conn)

	raw, ok, err := st.LoadCombatState(ctx, "campaign-persist")
	if err != nil {
		t.Fatalf("LoadCombatState() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadCombatState() ok = false, want true — start_combat should have persisted a snapshot")
	}
	var payload combatStatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshaling persisted combat state: %v", err)
	}
	if payload.TurnOrder == nil {
		t.Fatal("persisted turn_order is nil")
	}
	if !payload.TurnOrder.Active {
		t.Error("persisted turn_order.active = false, want true")
	}
	if len(payload.TurnOrder.Order) != 2 || payload.TurnOrder.Order[0] != "char-b" {
		t.Errorf("persisted turn_order.order = %v, want [char-b char-a]", payload.TurnOrder.Order)
	}
	if payload.TurnOrder.Round != 1 {
		t.Errorf("persisted turn_order.round = %d, want 1", payload.TurnOrder.Round)
	}
}

// TestCombatState_GenerateCombatMap_PersistsCombatMapSnapshot doesn't
// reuse startCombatAndGenerateMap (combat_map_test.go) since that helper
// doesn't return the underlying store — this test needs direct
// LoadCombatState access, so it drives the same start_combat +
// generate_combat_map sequence itself.
func TestCombatState_GenerateCombatMap_PersistsCombatMapSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fakeEngine := startCombatFakeEngine()
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "generate_combat_map", Arguments: json.RawMessage(`{}`)}}},
			{Text: "The dungeon room comes into view."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", "campaign-persist-map", "player-a", `{"name":"Kestrel","team":"party","combatStats":{"speed":30}}`)
	seedCharacterWithData(t, st, "char-b", "campaign-persist-map", "player-b", `{"name":"Grum","team":"monsters","combatStats":{"speed":30}}`)

	conn := dialAndJoin(t, ts, "campaign-persist-map", "player-a")
	defer conn.CloseNow()

	if _, err := sendPlayerInput(ctx, conn, "campaign-persist-map", "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	readMapTokenState(ctx, t, conn)
	drainUntilDMProse(ctx, t, conn)

	raw, ok, err := st.LoadCombatState(ctx, "campaign-persist-map")
	if err != nil {
		t.Fatalf("LoadCombatState() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadCombatState() ok = false, want true")
	}
	var payload combatStatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshaling persisted combat state: %v", err)
	}
	if payload.CombatMap == nil {
		t.Fatal("persisted combat_map is nil — generate_combat_map should have persisted it")
	}
	if len(payload.CombatMap.State.Tokens) != 2 {
		t.Errorf("persisted combat_map token count = %d, want 2", len(payload.CombatMap.State.Tokens))
	}
}

func TestCombatState_EndCombat_DeletesSnapshot(t *testing.T) {
	fakeEngine := startCombatFakeEngine()
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{Text: "Roll for initiative!"},
			{Text: "Kestrel sheathes her blade."},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "end_combat", Arguments: json.RawMessage(`{}`)}}},
			{Text: "The fight is over."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-persist-end", "player-a")
	seedCharacter(t, st, "char-b", "campaign-persist-end", "player-a")

	conn := dialAndJoin(t, ts, "campaign-persist-end", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-persist-end", "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble1 protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble1); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	readTurnState(ctx, t, conn)

	if _, ok, err := st.LoadCombatState(ctx, "campaign-persist-end"); err != nil || !ok {
		t.Fatalf("LoadCombatState() before end_combat: ok=%v err=%v, want ok=true", ok, err)
	}

	if _, err := sendPlayerInput(ctx, conn, "campaign-persist-end", "player-a", "char-a", "The fight winds down."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble2 protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble2); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	readTurnState(ctx, t, conn)

	_, ok, err := st.LoadCombatState(ctx, "campaign-persist-end")
	if err != nil {
		t.Fatalf("LoadCombatState() after end_combat error = %v", err)
	}
	if ok {
		t.Error("LoadCombatState() ok = true after end_combat, want false — the persisted snapshot should be deleted")
	}
}

// TestCombatState_SecondServerSharingStore_RehydratesAndCanAdvanceTurn is
// the actual "survives a Master restart" proof: a second *server.Server,
// sharing the same underlying store as the first but with its own,
// initially-empty in-memory turnOrders/combatMaps maps (exactly what a
// freshly-started Master process would have), calls WarmUpCombatState
// and is then able to advance the turn that was already in progress —
// proving the rehydrated state is genuinely usable, not just present on
// disk.
func TestCombatState_SecondServerSharingStore_RehydratesAndCanAdvanceTurn(t *testing.T) {
	fakeEngine := startCombatFakeEngine()
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{Text: "Roll for initiative!"},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-restart", "player-a")
	seedCharacter(t, st, "char-b", "campaign-restart", "player-a")

	conn := dialAndJoin(t, ts, "campaign-restart", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-restart", "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	firstState := readTurnState(ctx, t, conn)
	if firstState.CurrentCharacterID != "char-b" {
		t.Fatalf("setup: turn.state CurrentCharacterID = %q, want %q", firstState.CurrentCharacterID, "char-b")
	}

	// Simulate a Master restart: a brand-new Server sharing st, with its
	// own fresh (empty) in-memory maps — WarmUpCombatState is the only
	// thing standing between this and "no combat is active."
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fakeEngine2 := startCombatFakeEngine()
	srv2 := server.New(logger, st, fakeLLM, "test-model", nil, fakeEngine2, st, nil, nil, st, st, st, nil, nil, session.NewHub())
	if err := srv2.WarmUpCombatState(ctx); err != nil {
		t.Fatalf("WarmUpCombatState() error = %v", err)
	}

	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()
	conn2 := dialAndJoin(t, ts2, "campaign-restart", "player-a")
	defer conn2.CloseNow()

	fakeLLM.responses = append(fakeLLM.responses,
		llm.CompletionResponse{Text: "Char-b acts."},
		llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "advance_turn", Arguments: json.RawMessage(`{}`)}}},
		llm.CompletionResponse{Text: "Kestrel's turn comes around."},
	)
	if _, err := sendPlayerInput(ctx, conn2, "campaign-restart", "player-a", "char-b", "Char-b finishes acting."); err != nil {
		t.Fatalf("sendPlayerInput() on rehydrated server error = %v", err)
	}
	var bubble2 protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn2, &bubble2); err != nil {
		t.Fatalf("Read(narrative.player_bubble) on rehydrated server error = %v", err)
	}
	secondState := readTurnState(ctx, t, conn2)
	if secondState.CurrentCharacterID != "char-a" {
		t.Errorf("after rehydration, advance_turn CurrentCharacterID = %q, want %q — the pre-restart turn order should have carried over", secondState.CurrentCharacterID, "char-a")
	}
	if secondState.Round != 1 {
		t.Errorf("after rehydration, turn.state Round = %d, want 1", secondState.Round)
	}
}
