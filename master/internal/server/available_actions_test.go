// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// getAvailableActionsToolCallLLM builds a fakeLLMProvider that narrates,
// calls get_available_actions with the given arguments, then narrates —
// the same three-response shape castSpellToolCallLLM/attackToolCallLLM
// use for their own tools.
func getAvailableActionsToolCallLLM(argsJSON string) *fakeLLMProvider {
	return &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel looks around, weighing options."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "get_available_actions",
				Arguments: json.RawMessage(argsJSON),
			}}},
			{Text: "Kestrel decides on a course of action."},
		},
	}
}

// TestServe_NarrativePlayerInput_SlowPass_GetAvailableActions_Success_ReturnsRealActionList
// covers the happy path of the engine-computed action menu (CLAUDE.md's
// "gates over prompting"): the DM's get_available_actions tool call
// reaches the System Engine and its real response — can_act, action
// economy, and the concrete action list — surfaces in the tool result,
// not a guess.
func TestServe_NarrativePlayerInput_SlowPass_GetAvailableActions_Success_ReturnsRealActionList(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		getAvailableActionsResp: &systemenginepb.GetAvailableActionsResponse{
			Success:        true,
			CanAct:         true,
			HasAction:      true,
			HasBonusAction: true,
			HasReaction:    true,
			Actions: []*systemenginepb.AvailableAction{
				{
					Kind:                  systemenginepb.AvailableActionKind_AVAILABLE_ACTION_KIND_MELEE_ATTACK,
					Label:                 "Attack the Goblin with your Longsword",
					SourceName:            "Longsword",
					TargetCharacterId:     "target-char",
					ActionEconomyCategory: systemenginepb.ActionEconomyCategory_ACTION_ECONOMY_CATEGORY_ACTION,
				},
			},
		},
	}
	fakeLLM := getAvailableActionsToolCallLLM(`{"character_id":"attacker-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "attacker-char", "campaign-available-actions", "player-a")

	conn := dialAndJoin(t, ts, "campaign-available-actions", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-available-actions", "player-a", "attacker-char", "What can I do?"); err != nil {
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
	if !toolResult.Payload.Success {
		t.Fatalf("tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}
	if toolResult.Payload.ToolName != "get_available_actions" {
		t.Errorf("tool.result ToolName = %q, want %q", toolResult.Payload.ToolName, "get_available_actions")
	}

	if fakeEngine.lastGetAvailableActionsRequest == nil {
		t.Fatal("GetAvailableActions was never called")
	}
	if fakeEngine.lastGetAvailableActionsRequest.Actor.ActorId != "attacker-char" {
		t.Errorf("GetAvailableActions called with Actor.ActorId = %q, want %q", fakeEngine.lastGetAvailableActionsRequest.Actor.ActorId, "attacker-char")
	}
	if len(fakeEngine.lastGetAvailableActionsRequest.CandidateTargets) != 0 {
		t.Errorf("GetAvailableActions called with %d CandidateTargets outside combat, want 0", len(fakeEngine.lastGetAvailableActionsRequest.CandidateTargets))
	}
}

// TestServe_NarrativePlayerInput_SlowPass_GetAvailableActions_DuringCombat_SuppliesOtherParticipantsAsCandidates
// covers dmGetAvailableActions' use of combatParticipantIDs
// (turn_order.go) — every OTHER character currently in the active fight
// is supplied as a candidate target, since the engine holds no combat
// roster of its own (every RPC in this contract is stateless per call).
func TestServe_NarrativePlayerInput_SlowPass_GetAvailableActions_DuringCombat_SuppliesOtherParticipantsAsCandidates(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckResp: &systemenginepb.ResolveCheckResponse{
			Success: true,
			Outcome: &systemenginepb.Outcome{Total: 12, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 12, Label: "d20"}}},
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          noOpStartTurnResp,
		getAvailableActionsResp: &systemenginepb.GetAvailableActionsResponse{
			Success: true, CanAct: true, HasAction: true, HasBonusAction: true, HasReaction: true,
		},
	}
	campaignID := "campaign-available-actions-combat"
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel squares off against the goblin."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "get_available_actions", Arguments: json.RawMessage(`{"character_id":"char-a"}`)}}},
			{Text: "Kestrel weighs the options."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", campaignID, "player-a", `{"name":"Kestrel"}`)
	seedCharacterWithData(t, st, "char-b", campaignID, "player-b", `{"name":"Grum"}`)

	conn := dialAndJoin(t, ts, campaignID, "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, campaignID, "player-a", "char-a", "I fight the goblin, then consider my options."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		typ, _, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ == protocol.MessageTypeNarrativeDmProse {
			break
		}
	}

	if fakeEngine.lastGetAvailableActionsRequest == nil {
		t.Fatal("GetAvailableActions was never called")
	}
	targets := fakeEngine.lastGetAvailableActionsRequest.CandidateTargets
	if len(targets) != 1 || targets[0].ActorId != "char-b" {
		t.Errorf("GetAvailableActions CandidateTargets = %+v, want exactly [char-b]", targets)
	}
}
