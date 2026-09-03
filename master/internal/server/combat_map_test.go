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
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// seedCharacterWithData is seedCharacter (roll_check_test.go) with
// caller-supplied character_data, for tests that need real name/team/
// combatStats.speed fields — combat-map tests need those (see
// characterCombatInfo's doc comment for why the server package reads
// them directly), unlike most of this package's tests which only need a
// character to exist at all.
func seedCharacterWithData(t *testing.T, st *store.SQLiteEventStore, id, campaignID, ownerID, characterData string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.SaveCharacter(context.Background(), store.Character{
		ID:            id,
		CampaignID:    campaignID,
		OwnerID:       ownerID,
		SchemaVersion: "opencombatengine-v1",
		Status:        store.CharacterStatusPendingReview,
		CharacterData: json.RawMessage(characterData),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
}

// readMapTokenState reads messages from conn until a map.token_state
// arrives (or 20 messages pass), the same "read generically by type"
// approach readTurnState (turn_order_test.go) already uses.
func readMapTokenState(ctx context.Context, t *testing.T, conn *websocket.Conn) protocol.MapTokenStatePayload {
	t.Helper()
	for i := 0; i < 20; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ != protocol.MessageTypeMapTokenState {
			continue
		}
		var msg protocol.MapTokenStateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshaling map.token_state: %v", err)
		}
		return msg.Payload
	}
	t.Fatal("no map.token_state message arrived within 20 messages")
	return protocol.MapTokenStatePayload{}
}

// drainUntilDMProse reads and discards messages from conn until
// narrative.dm_prose arrives (or 20 messages pass) — the slow pass's own
// final message, so a caller that only cares about an earlier message
// (e.g. its own map.token_state) can still leave the connection's queue
// empty afterward rather than leaking trailing broadcast traffic
// (tool.result for a later tool call, the final narration itself) into
// whatever the test reads next.
func drainUntilDMProse(ctx context.Context, t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for i := 0; i < 20; i++ {
		typ, _, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ == protocol.MessageTypeNarrativeDmProse {
			return
		}
	}
	t.Fatal("no narrative.dm_prose message arrived within 20 messages")
}

// startCombatAndGenerateMap drives a single slow-pass turn through two DM
// tool calls (start_combat then generate_combat_map) and returns both
// players' own map.token_state — the shared setup every test in this
// file needs, so combat is genuinely active (dmGenerateCombatMap requires
// it) rather than each test re-deriving that sequence.
func startCombatAndGenerateMap(t *testing.T, campaignID string) (tsClose func(), connA, connB *websocket.Conn, stateA, stateB protocol.MapTokenStatePayload) {
	t.Helper()
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{Total: 10, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 10, Label: "d20"}}},
			}, nil
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          noOpStartTurnResp,
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."}, // fast pass
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "generate_combat_map", Arguments: json.RawMessage(`{}`)}}},
			{Text: "The dungeon room comes into view."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	seedCharacterWithData(t, st, "char-a", campaignID, "player-a", `{"name":"Kestrel","team":"party","combatStats":{"speed":30}}`)
	seedCharacterWithData(t, st, "char-b", campaignID, "player-b", `{"name":"Grum","team":"monsters","combatStats":{"speed":30}}`)

	connA = dialAndJoin(t, ts, campaignID, "player-a")
	connB = dialAndJoin(t, ts, campaignID, "player-b")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, connA, campaignID, "player-a", "char-a", "We spring the trap!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	// Each connection's own map.token_state arrives partway through the
	// slow pass (generate_combat_map's own tool.result and the final
	// narrative.dm_prose still follow it) — drain the rest so a caller
	// using connA/connB afterward starts with an empty queue, not
	// leftover broadcast traffic from this setup turn.
	stateA = readMapTokenState(ctx, t, connA)
	drainUntilDMProse(ctx, t, connA)
	stateB = readMapTokenState(ctx, t, connB)
	drainUntilDMProse(ctx, t, connB)

	return ts.Close, connA, connB, stateA, stateB
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateCombatMap_SendsDifferentFogOfWarPerPlayer(t *testing.T) {
	closeTS, connA, connB, stateA, stateB := startCombatAndGenerateMap(t, "campaign-map-fog")
	defer closeTS()
	defer connA.CloseNow()
	defer connB.CloseNow()

	if stateA.ImageURL == "" {
		t.Fatal("player-a's map.token_state ImageURL is empty")
	}
	if stateB.ImageURL == "" {
		t.Fatal("player-b's map.token_state ImageURL is empty")
	}
	if stateA.ImageURL == stateB.ImageURL {
		t.Error("player-a and player-b received identical map.token_state ImageURL, want different fog-of-war views (their characters are clustered on opposite sides of the generated map)")
	}
	if len(stateA.Grid.Cells) == 0 {
		t.Error("player-a's visible grid.cells is empty, want at least their own token's cell visible")
	}
	if len(stateA.Tokens) == 0 {
		t.Error("player-a's visible tokens is empty, want at least their own token visible")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateCombatMap_NoActiveCombat_FailsToolCall(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel looks around."}, // fast pass
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "generate_combat_map", Arguments: json.RawMessage(`{}`)}}},
			{Text: "Nothing structured happens."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, &fakeSystemEngineClient{})
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", "campaign-map-noco", "player-a", `{"name":"Kestrel","team":"party"}`)

	conn := dialAndJoin(t, ts, "campaign-map-noco", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-map-noco", "player-a", "char-a", "I look around the room."); err != nil {
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
		t.Error("tool.result Success = true, want false (no active combat to generate a map for)")
	}
	if toolResult.Payload.ReasonCode != "no_active_combat" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "no_active_combat")
	}
}

func requestMapTokenMove(ctx context.Context, conn *websocket.Conn, sender, campaignID, tokenID string, to protocol.GridPositionPayload) error {
	msg := protocol.MapTokenMoveRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "move-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeMapTokenMoveRequest,
		},
		Payload: protocol.MapTokenMoveRequestPayload{TokenID: tokenID, To: to},
	}
	return wsjson.Write(ctx, conn, msg)
}

func TestServe_MapTokenMoveRequest_ToOwnCurrentPosition_SucceedsAndResendsState(t *testing.T) {
	closeTS, connA, connB, stateA, _ := startCombatAndGenerateMap(t, "campaign-map-move-same")
	defer closeTS()
	defer connA.CloseNow()
	defer connB.CloseNow()

	var ownToken protocol.TokenPayload
	for _, tok := range stateA.Tokens {
		if tok.CharacterID == "char-a" {
			ownToken = tok
		}
	}
	if ownToken.TokenID == "" {
		t.Fatal("player-a's own token (char-a) not found in their map.token_state")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestMapTokenMove(ctx, connA, "player-a", "campaign-map-move-same", ownToken.TokenID, ownToken.Position); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}

	updated := readMapTokenState(ctx, t, connA)
	var movedToken protocol.TokenPayload
	for _, tok := range updated.Tokens {
		if tok.CharacterID == "char-a" {
			movedToken = tok
		}
	}
	if movedToken.Position != ownToken.Position {
		t.Errorf("token position after a same-cell move = %+v, want unchanged %+v", movedToken.Position, ownToken.Position)
	}
}

func TestServe_MapTokenMoveRequest_FarOutOfRange_RespondsWithSystemError(t *testing.T) {
	closeTS, connA, connB, stateA, _ := startCombatAndGenerateMap(t, "campaign-map-move-far")
	defer closeTS()
	defer connA.CloseNow()
	defer connB.CloseNow()

	var ownToken protocol.TokenPayload
	for _, tok := range stateA.Tokens {
		if tok.CharacterID == "char-a" {
			ownToken = tok
		}
	}
	if ownToken.TokenID == "" {
		t.Fatal("player-a's own token (char-a) not found in their map.token_state")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	farAway := protocol.GridPositionPayload{X: ownToken.Position.X + 1000, Y: ownToken.Position.Y + 1000}
	if err := requestMapTokenMove(ctx, connA, "player-a", "campaign-map-move-far", ownToken.TokenID, farAway); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, connA)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (destination is wildly out of movement range)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_MapTokenMoveRequest_NotOwnedByRequester_RespondsWithSystemError(t *testing.T) {
	closeTS, connA, connB, stateA, _ := startCombatAndGenerateMap(t, "campaign-map-move-notowned")
	defer closeTS()
	defer connA.CloseNow()
	defer connB.CloseNow()

	var charAToken protocol.TokenPayload
	for _, tok := range stateA.Tokens {
		if tok.CharacterID == "char-a" {
			charAToken = tok
		}
	}
	if charAToken.TokenID == "" {
		t.Fatal("char-a's token not found in player-a's map.token_state")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// player-b (owns char-b) tries to move char-a's token, which they
	// don't own.
	if err := requestMapTokenMove(ctx, connB, "player-b", "campaign-map-move-notowned", charAToken.TokenID, charAToken.Position); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, connB)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (player-b does not own char-a's token)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_MapTokenMoveRequest_UnknownToken_RespondsWithSystemError(t *testing.T) {
	closeTS, connA, connB, _, _ := startCombatAndGenerateMap(t, "campaign-map-move-unknown")
	defer closeTS()
	defer connA.CloseNow()
	defer connB.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestMapTokenMove(ctx, connA, "player-a", "campaign-map-move-unknown", "no-such-token", protocol.GridPositionPayload{X: 0, Y: 0}); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, connA)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (token does not exist)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_MapTokenMoveRequest_NoActiveMap_RespondsWithSystemError(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "unused"}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, &fakeSystemEngineClient{})
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", "campaign-map-none", "player-a", `{"name":"Kestrel"}`)

	conn := dialAndJoin(t, ts, "campaign-map-none", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestMapTokenMove(ctx, conn, "player-a", "campaign-map-none", "tok-1", protocol.GridPositionPayload{X: 0, Y: 0}); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (no combat map has ever been generated for this campaign)", typ, protocol.MessageTypeSystemError)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_EndCombat_ClearsCombatMap drives
// start_combat, generate_combat_map, and end_combat as three tool calls in
// one slow pass, then attempts a token move — which must now fail with
// no_active_map, proving turn_order.go's endCombat actually deleted
// s.combatMaps[campaignID] (not just s.turnOrders, which
// TestServe_NarrativePlayerInput_SlowPass_EndCombat_BroadcastsInactiveTurnState
// in turn_order_test.go already covers on its own).
func TestServe_NarrativePlayerInput_SlowPass_EndCombat_ClearsCombatMap(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{Total: 10, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 10, Label: "d20"}}},
			}, nil
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          noOpStartTurnResp,
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws steel."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "generate_combat_map", Arguments: json.RawMessage(`{}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_3", Name: "end_combat", Arguments: json.RawMessage(`{}`)}}},
			{Text: "The fight is over as quickly as it began."},
		},
	}
	campaignID := "campaign-map-endcombat"
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", campaignID, "player-a", `{"name":"Kestrel","team":"party","combatStats":{"speed":30}}`)
	seedCharacterWithData(t, st, "char-b", campaignID, "player-b", `{"name":"Grum","team":"monsters","combatStats":{"speed":30}}`)

	conn := dialAndJoin(t, ts, campaignID, "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, campaignID, "player-a", "char-a", "We spring the trap, then the goblin flees!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	mapState := readMapTokenState(ctx, t, conn)
	var ownToken protocol.TokenPayload
	for _, tok := range mapState.Tokens {
		if tok.CharacterID == "char-a" {
			ownToken = tok
		}
	}
	if ownToken.TokenID == "" {
		t.Fatal("player-a's own token (char-a) not found in their map.token_state")
	}
	// generate_combat_map's tool.result, end_combat's tool.result and
	// turn.state, and the final narrative.dm_prose all still follow —
	// drain them so the move request below reads a fresh response, not
	// leftover traffic from this setup turn.
	drainUntilDMProse(ctx, t, conn)

	if err := requestMapTokenMove(ctx, conn, "player-a", campaignID, ownToken.TokenID, ownToken.Position); err != nil {
		t.Fatalf("requestMapTokenMove() error = %v", err)
	}
	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (end_combat should have cleared the combat map)", typ, protocol.MessageTypeSystemError)
	}
}
