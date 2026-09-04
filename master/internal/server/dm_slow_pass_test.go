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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/imagegen"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// newTestServerWithLLMAndSystemEngine builds a Server with a real
// in-memory SQLite store, an llm.Provider, and (optionally, may be nil)
// a system engine client — enough to exercise the DM slow pass
// end-to-end (design doc §7, §8) without a real Ollama server or gRPC
// sidecar. policyProvider is optional (variadic so existing call sites
// with none don't need updating) — pass one to test governance-gate
// behavior (design doc §9.1, §9.5); omitted, campaigns get
// policy.Default().
func newTestServerWithLLMAndSystemEngine(t *testing.T, llmProvider llm.Provider, fakeEngine *fakeSystemEngineClient, policyProvider ...policy.Provider) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var policyP policy.Provider
	if len(policyProvider) > 0 {
		policyP = policyProvider[0]
	}

	var systemEngineClient systemenginepb.SystemEngineClient
	if fakeEngine != nil {
		systemEngineClient = fakeEngine
	}
	ts := httptest.NewServer(server.New(logger, st, llmProvider, "test-model", nil, systemEngineClient, st, policyP, nil, st, st, st).Handler())
	return ts, st
}

// newTestServerWithLLMSystemEngineAndImageGen is like
// newTestServerWithLLMAndSystemEngine but also wires an
// imagegen.Provider — a separate helper rather than a second variadic
// parameter, since Go allows only one variadic parameter per function.
func newTestServerWithLLMSystemEngineAndImageGen(t *testing.T, llmProvider llm.Provider, fakeEngine *fakeSystemEngineClient, imageGenProvider imagegen.Provider) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var systemEngineClient systemenginepb.SystemEngineClient
	if fakeEngine != nil {
		systemEngineClient = fakeEngine
	}
	ts := httptest.NewServer(server.New(logger, st, llmProvider, "test-model", nil, systemEngineClient, st, nil, imageGenProvider, st, st, st).Handler())
	return ts, st
}

// newTestServerWithLLMSystemEngineImageGenAndPolicy wires all four —
// used by the one test that needs both a policy.Provider and an
// imagegen.Provider together.
func newTestServerWithLLMSystemEngineImageGenAndPolicy(t *testing.T, llmProvider llm.Provider, fakeEngine *fakeSystemEngineClient, imageGenProvider imagegen.Provider, policyProvider policy.Provider) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var systemEngineClient systemenginepb.SystemEngineClient
	if fakeEngine != nil {
		systemEngineClient = fakeEngine
	}
	ts := httptest.NewServer(server.New(logger, st, llmProvider, "test-model", nil, systemEngineClient, st, policyProvider, imageGenProvider, st, st, st).Handler())
	return ts, st
}

func sendPlayerInput(ctx context.Context, conn *websocket.Conn, campaignID, sender, characterID, text string) (protocol.NarrativePlayerInputMessage, error) {
	input := protocol.NarrativePlayerInputMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "input-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeNarrativePlayerInput,
		},
		Payload: protocol.NarrativePlayerInputPayload{CharacterID: characterID, Text: text, Source: protocol.NarrativeInputSourceTyped},
	}
	return input, wsjson.Write(ctx, conn, input)
}

func TestServe_NarrativePlayerInput_SlowPass_NoSystemEngine_OmitsToolsAndBroadcastsDmProse(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws a sword."},            // fast pass
			{Text: "The blade catches the torchlight."}, // slow pass — no system engine, so no tools offered
		},
	}
	// Deliberately not newTestServerWithLLMAndSystemEngine: that helper
	// always wires a real store for combatState/campaignPack/vehicles,
	// and vehicle tools (unlike campaign-pack tools) are gated
	// independently of systemEngine — they'd still be offered even here.
	// This test's actual intent is narrower: a deployment with nothing
	// but an LLM configured gets no tools at all.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, fakeLLM, "test-model", nil, nil, nil, nil, nil, nil, nil, nil).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-slow", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input, err := sendPlayerInput(ctx, conn, "campaign-slow", "player-a", "char-a", "I draw my sword.")
	if err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}
	if prose.Payload.Text != "The blade catches the torchlight." {
		t.Errorf("Payload.Text = %q, want %q", prose.Payload.Text, "The blade catches the torchlight.")
	}
	if prose.Payload.InReplyToMessageID != input.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", prose.Payload.InReplyToMessageID, input.MessageID)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	if len(slowPassCall.Tools) != 0 {
		t.Errorf("slow pass call Tools = %+v, want none (no system engine configured)", slowPassCall.Tools)
	}
	if len(slowPassCall.Messages) != 2 {
		t.Fatalf("slow pass call Messages length = %d, want 2 (system + user)", len(slowPassCall.Messages))
	}
	// The character ID rides along explicitly (not just the raw text) —
	// caught by real end-to-end testing that the model has no other way
	// to know which ID to pass to a tool call, and guesses wrong without it.
	wantContent := "Character ID: char-a\nPlayer action: I draw my sword."
	if slowPassCall.Messages[1].Content != wantContent {
		t.Errorf("slow pass user message = %q, want %q", slowPassCall.Messages[1].Content, wantContent)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CharacterFound_IncludesCharacterDataInContext
// exercises the character-context feature added so the DM has something
// real to judge action feasibility against (a feature not listed,
// movement past combatStats.speed, ... — see dmSlowPassSystemPrompt)
// instead of the ungrounded guess it made before this existed. Spell
// feasibility specifically is now enforced by cast_spell's hard gate
// rather than judged from this context (see cast_spell_test.go).
func TestServe_NarrativePlayerInput_SlowPass_CharacterFound_IncludesCharacterDataInContext(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws a sword."},            // fast pass
			{Text: "The blade catches the torchlight."}, // slow pass
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-context", "player-a")

	conn := dialAndJoin(t, ts, "campaign-context", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-context", "player-a", "char-1", "I draw my sword."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	if len(slowPassCall.Messages) != 2 {
		t.Fatalf("slow pass call Messages length = %d, want 2 (system + user)", len(slowPassCall.Messages))
	}
	userContent := slowPassCall.Messages[1].Content
	wantContent := "Character ID: char-1\nCharacter data: {\"name\":\"Kestrel\"}\nPlayer action: I draw my sword."
	if userContent != wantContent {
		t.Errorf("slow pass user message = %q, want %q", userContent, wantContent)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CharacterNotFound_OmitsDataButStillNarrates
// covers the graceful-degradation path: a character_id that doesn't
// resolve (a bad ID, a character not yet uploaded) shouldn't fail the
// whole turn — the model still has the ID and the stated action to work
// with, same as before the character-context feature existed.
func TestServe_NarrativePlayerInput_SlowPass_CharacterNotFound_OmitsDataButStillNarrates(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Someone draws a sword."},            // fast pass
			{Text: "The blade catches the torchlight."}, // slow pass
		},
	}
	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	// Deliberately not seeded — char-does-not-exist has no store row.

	conn := dialAndJoin(t, ts, "campaign-context-missing", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-context-missing", "player-a", "char-does-not-exist", "I draw my sword."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}
	if prose.Payload.Text != "The blade catches the torchlight." {
		t.Errorf("narrative.dm_prose Text = %q, want the turn to still complete normally", prose.Payload.Text)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	userContent := slowPassCall.Messages[1].Content
	wantContent := "Character ID: char-does-not-exist\nPlayer action: I draw my sword."
	if userContent != wantContent {
		t.Errorf("slow pass user message = %q, want %q (no Character data section)", userContent, wantContent)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_MalformedToolCallText_DoesNotBroadcast
// is grounded in a real failure observed against the actual LAN Ollama
// server (qwen2.5:32b): the model sometimes emits a failed tool-call
// attempt as plain narration text — a JSON blob wrapped in a (sometimes
// garbled) <tool_call> tag — instead of populating the structured
// tool-call field. Broadcasting that verbatim to the whole table would
// violate CLAUDE.md's "gates over prompting" rule, so runSlowPass must
// recognize it (looksLikeMalformedToolCall) and broadcast nothing rather
// than the raw artifact.
func TestServe_NarrativePlayerInput_SlowPass_MalformedToolCallText_DoesNotBroadcast(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel begins a training drill."}, // fast pass
			{Text: "rPid\n{\n\"name\": \"resolve_check\",\n\"arguments\": {\n\"character_id\": \"char-a\"\n}\n}\n</tool_call>"},
		},
	}
	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-slow-malformed", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-slow-malformed", "player-a", "char-a", "I train."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()
	if _, _, err := readEnvelopeType(shortCtx, conn); err == nil {
		t.Fatal("expected no further message after the malformed slow-pass response, but one arrived")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ToolCall_BroadcastsRollToolResultAndDmProse(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{
				Total:         14,
				ResultSummary: "resolved",
				Rolls:         []*systemenginepb.DieRoll{{Sides: 20, Result: 11, Label: "d20"}},
			},
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel tries to climb the crumbling wall."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "resolve_check",
				Arguments: json.RawMessage(`{"character_id":"char-1","check_type":"ability_check","ability":"Strength"}`),
			}}}, // slow pass turn 1: calls a tool
			{Text: "Kestrel's fingers slip, but they catch a ledge just in time."}, // slow pass turn 2: final narration
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-slow", "player-a")

	conn := dialAndJoin(t, ts, "campaign-slow", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-slow", "player-a", "char-1", "I climb the crumbling wall."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	// The slow pass's tool call produces roll.request/roll.result (a
	// DM-triggered check is a shared table event, same as a player's own
	// roll.check_request), a tool.result, and finally narrative.dm_prose
	// — read generically by type rather than assuming a strict order,
	// since only "all four eventually arrive" is the actual contract.
	var sawRollRequest, sawRollResult, sawToolResult bool
	var prose protocol.NarrativeDmProseMessage
	for i := 0; i < 10; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d error = %v", i, err)
		}
		switch typ {
		case protocol.MessageTypeRollRequest:
			sawRollRequest = true
		case protocol.MessageTypeRollResult:
			sawRollResult = true
			var rr protocol.RollResultMessage
			if err := json.Unmarshal(data, &rr); err != nil {
				t.Fatalf("unmarshaling roll.result error = %v", err)
			}
			if rr.Payload.Total != 14 {
				t.Errorf("roll.result Total = %d, want 14", rr.Payload.Total)
			}
		case protocol.MessageTypeToolResult:
			sawToolResult = true
			var tr protocol.ToolResultMessage
			if err := json.Unmarshal(data, &tr); err != nil {
				t.Fatalf("unmarshaling tool.result error = %v", err)
			}
			if tr.Payload.ToolName != "resolve_check" {
				t.Errorf("tool.result ToolName = %q, want %q", tr.Payload.ToolName, "resolve_check")
			}
			if !tr.Payload.Success {
				t.Errorf("tool.result Success = false, want true")
			}
			if tr.Payload.Caller != "dm" {
				t.Errorf("tool.result Caller = %q, want %q", tr.Payload.Caller, "dm")
			}
		case protocol.MessageTypeNarrativeDmProse:
			if err := json.Unmarshal(data, &prose); err != nil {
				t.Fatalf("unmarshaling narrative.dm_prose error = %v", err)
			}
		default:
			t.Fatalf("unexpected message type %q", typ)
		}
		if prose.Payload.Text != "" {
			break
		}
	}

	if !sawRollRequest || !sawRollResult || !sawToolResult {
		t.Errorf("sawRollRequest=%v sawRollResult=%v sawToolResult=%v, want all true", sawRollRequest, sawRollResult, sawToolResult)
	}
	if prose.Payload.Text != "Kestrel's fingers slip, but they catch a ledge just in time." {
		t.Errorf("narrative.dm_prose Text = %q, want %q", prose.Payload.Text, "Kestrel's fingers slip, but they catch a ledge just in time.")
	}

	// The tool result fed back to the model's second slow-pass call must
	// carry the real resolved total, not a placeholder.
	secondSlowPassCall := fakeLLM.callAt(t, 2)
	lastMsg := secondSlowPassCall.Messages[len(secondSlowPassCall.Messages)-1]
	if lastMsg.Role != llm.RoleTool || lastMsg.ToolCallID != "call_1" {
		t.Fatalf("last message before final narration = %+v, want a RoleTool reply to call_1", lastMsg)
	}
	var toolResultPayload map[string]any
	if err := json.Unmarshal([]byte(lastMsg.Content), &toolResultPayload); err != nil {
		t.Fatalf("unmarshaling tool result content error = %v", err)
	}
	if toolResultPayload["total"] != 14.0 {
		t.Errorf("tool result content total = %v, want 14", toolResultPayload["total"])
	}
}

// TestServe_NarrativePlayerInput_SlowPass_StartCombatFails_ClaimsTurnOrderAnyway_DoesNotBroadcast
// reproduces a real failure observed live against a real Ollama server
// (qwen2.5:32b) and a real OpenCombatEngine sidecar: start_combat failed
// (here, because one of the given character_ids doesn't exist — the same
// underlying cause as a create_npc call for that combatant having failed
// earlier), the failure was broadcast as a genuine tool.result, and the
// model's final narration nonetheless announced "the initiative is
// rolled" and who goes first — directly contradicting both the tool
// result it was just handed and dm_slow_pass.go's own system-prompt
// instruction not to do this. runSlowPass's turnOrderCallFailed +
// looksLikeUnearnedTurnOrderClaim backstop must catch this and broadcast
// nothing, the same "no usable narration this turn" treatment
// looksLikeMalformedToolCall already gets.
func TestServe_NarrativePlayerInput_SlowPass_StartCombatFails_ClaimsTurnOrderAnyway_DoesNotBroadcast(t *testing.T) {
	// char-1 (the first, real ID in the tool call below) must clear every
	// step startCombat takes before it reaches char-npc-never-created and
	// fails there — a status check, an initiative roll, and (once
	// initiative order is established) a StartTurn call for whoever ends
	// up first — so all three need a valid canned response, same as
	// turn_order_test.go's own combat-happy-path fakes.
	fakeEngine := &fakeSystemEngineClient{
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 15, ResultSummary: "resolved"},
		},
		startTurnResp: noOpStartTurnResp,
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Four goblin scouts leap from the scrub."}, // fast pass
			{ToolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "start_combat",
				Arguments: json.RawMessage(`{"character_ids":["char-1","char-npc-never-created"]}`),
			}}}, // slow pass: start_combat with an ID that was never actually created
			{Text: "The initiative is rolled and it seems the Goblin Scouts strike first."}, // final narration, despite the failure above
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-slow-turn-order", "player-a")

	conn := dialAndJoin(t, ts, "campaign-slow-turn-order", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-slow-turn-order", "player-a", "char-1", "Goblins attack!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	// char-1 (the tool call's first, real ID) rolls a genuine initiative
	// check before the loop ever reaches char-npc-never-created and
	// fails — so a roll.request/roll.result pair for that roll is
	// expected here too, not just the tool.result; read generically by
	// type (same approach the ToolCall_BroadcastsRollToolResultAndDmProse
	// test above uses) rather than assuming a strict order.
	var sawToolResult bool
	var toolResult protocol.ToolResultMessage
	for i := 0; i < 10 && !sawToolResult; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d error = %v", i, err)
		}
		if typ == protocol.MessageTypeToolResult {
			sawToolResult = true
			if err := json.Unmarshal(data, &toolResult); err != nil {
				t.Fatalf("unmarshaling tool.result error = %v", err)
			}
		}
	}
	if !sawToolResult {
		t.Fatal("never saw a tool.result message")
	}
	if toolResult.Payload.Success {
		t.Fatalf("tool.result Success = true, want false (start_combat should have failed on a nonexistent character_id)")
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()
	if _, _, err := readEnvelopeType(shortCtx, conn); err == nil {
		t.Fatal("expected no narrative.dm_prose after start_combat failed and the model claimed turn order anyway, but a message arrived")
	}
}
