// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// TestServe_NarrativePlayerInput_SlowPass_ApplyEffect_AmountBounds covers
// a real gap a security review found: the PvP gate only fires for damage
// against a different player's character, so a self-targeting heal/buff
// (or an NPC/master-targeted effect) had no magnitude check at all —
// meaning a player who successfully prompt-injects the DM model into
// calling apply_effect with an absurd amount for their own character had
// nothing in Master's own code stopping it. This must be enforced in
// code before the effect reaches the system engine (CLAUDE.md's "gates
// over prompting"), never left to the model's own restraint.
func TestServe_NarrativePlayerInput_SlowPass_ApplyEffect_AmountBounds(t *testing.T) {
	tests := []struct {
		name           string
		amount         int
		wantSuccess    bool
		wantReasonCode string
	}{
		{name: "WithinBounds_Succeeds", amount: 50, wantSuccess: true},
		{name: "ExactlyAtBound_Succeeds", amount: 1000, wantSuccess: true},
		{name: "OverBound_Blocked", amount: 1001, wantSuccess: false, wantReasonCode: "amount_out_of_range"},
		{name: "WayOverBound_Blocked", amount: 999999999, wantSuccess: false, wantReasonCode: "amount_out_of_range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				applyEffectResp: &systemenginepb.ApplyEffectResponse{
					Success: true,
					Actor:   &systemenginepb.Actor{ActorId: "char-a", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
				getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
			}
			fakeLLM := &fakeLLMProvider{
				responses: []llm.CompletionResponse{
					{Text: "Kestrel acts."},
					{ToolCalls: []llm.ToolCall{{
						ID:        "call_1",
						Name:      "apply_effect",
						Arguments: json.RawMessage(mustJSON(t, map[string]any{"character_id": "char-a", "effect_type": "heal", "amount": tt.amount})),
					}}},
					{Text: "The dust settles."},
				},
			}

			var ts *httptest.Server
			var st *store.SQLiteEventStore
			ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
			defer ts.Close()
			seedCharacter(t, st, "char-a", "campaign-bounds", "player-a")

			conn := dialAndJoin(t, ts, "campaign-bounds", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-bounds", "player-a", "char-a", "I rest."); err != nil {
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

			if toolResult.Payload.Success != tt.wantSuccess {
				t.Errorf("tool.result Success = %v, want %v (payload: %+v)", toolResult.Payload.Success, tt.wantSuccess, toolResult.Payload)
			}
			if tt.wantReasonCode != "" && toolResult.Payload.ReasonCode != tt.wantReasonCode {
				t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, tt.wantReasonCode)
			}
			if !tt.wantSuccess && fakeEngine.lastApplyEffectRequest != nil {
				t.Error("ApplyEffect was called, want the amount-bounds gate to have blocked the call before it reached the system engine")
			}
		})
	}
}

// TestServe_NarrativePlayerInput_SlowPass_AddCurrency_AmountBounds covers
// the same class of gap for add_currency — no gate at all previously
// stopped an arbitrarily large (or negative) currency amount from
// reaching the system engine.
func TestServe_NarrativePlayerInput_SlowPass_AddCurrency_AmountBounds(t *testing.T) {
	tests := []struct {
		name           string
		gold           int
		wantSuccess    bool
		wantReasonCode string
	}{
		{name: "WithinBounds_Succeeds", gold: 500, wantSuccess: true},
		{name: "ExactlyAtBound_Succeeds", gold: 100000, wantSuccess: true},
		{name: "OverBound_Blocked", gold: 100001, wantSuccess: false, wantReasonCode: "amount_out_of_range"},
		{name: "WayOverBound_Blocked", gold: 2000000000, wantSuccess: false, wantReasonCode: "amount_out_of_range"},
		{name: "Negative_Blocked", gold: -1, wantSuccess: false, wantReasonCode: "amount_out_of_range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				addCurrencyResp: &systemenginepb.AddCurrencyResponse{
					Success: true,
					Actor:   &systemenginepb.Actor{ActorId: "char-a", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
			}
			fakeLLM := &fakeLLMProvider{
				responses: []llm.CompletionResponse{
					{Text: "Kestrel searches."},
					{ToolCalls: []llm.ToolCall{{
						ID:        "call_1",
						Name:      "add_currency",
						Arguments: json.RawMessage(mustJSON(t, map[string]any{"character_id": "char-a", "gold": tt.gold})),
					}}},
					{Text: "The chest is empty now."},
				},
			}

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
			defer ts.Close()
			seedCharacter(t, st, "char-a", "campaign-bounds", "player-a")

			conn := dialAndJoin(t, ts, "campaign-bounds", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-bounds", "player-a", "char-a", "I search the chest."); err != nil {
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

			if toolResult.Payload.Success != tt.wantSuccess {
				t.Errorf("tool.result Success = %v, want %v (payload: %+v)", toolResult.Payload.Success, tt.wantSuccess, toolResult.Payload)
			}
			if tt.wantReasonCode != "" && toolResult.Payload.ReasonCode != tt.wantReasonCode {
				t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, tt.wantReasonCode)
			}
			if !tt.wantSuccess && fakeEngine.lastAddCurrencyRequest != nil {
				t.Error("AddCurrency was called, want the amount-bounds gate to have blocked the call before it reached the system engine")
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling test JSON: %v", err)
	}
	return b
}
