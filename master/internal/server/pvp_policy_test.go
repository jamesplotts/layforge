// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// TestServe_NarrativePlayerInput_SlowPass_ApplyEffect_PvPGate covers
// design doc §9.1's PvP policy matrix end-to-end through the real DM
// tool-call path (dmApplyEffect) — never left to the DM model's own
// judgment (CLAUDE.md's "gates over prompting"). Every case sends the
// same player action; only the target's owner, the effect type, and the
// campaign's configured policy vary.
func TestServe_NarrativePlayerInput_SlowPass_ApplyEffect_PvPGate(t *testing.T) {
	tests := []struct {
		name           string
		targetOwner    string // "player-a" (acting player's own character), "player-b" (a different player), "master" (an NPC)
		effectType     string
		policies       map[string]policy.CampaignPolicy // nil = no provider configured at all
		wantSuccess    bool
		wantReasonCode string
	}{
		{
			name:           "DamageDifferentPlayer_PveOnly_Blocked",
			targetOwner:    "player-b",
			effectType:     "damage",
			policies:       map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
		{
			name:        "DamageDifferentPlayer_Allowed_Succeeds",
			targetOwner: "player-b",
			effectType:  "damage",
			policies:    map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess: true,
		},
		{
			name:        "DamageDifferentPlayer_WithConsent_ConsentGranted_Succeeds",
			targetOwner: "player-b",
			effectType:  "damage",
			policies:    map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyWithConsent, PvPConsent: []string{"player-b"}}},
			wantSuccess: true,
		},
		{
			name:           "DamageDifferentPlayer_WithConsent_NoConsent_Blocked",
			targetOwner:    "player-b",
			effectType:     "damage",
			policies:       map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyWithConsent}},
			wantSuccess:    false,
			wantReasonCode: "pvp_no_consent",
		},
		{
			name:        "DamageOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner: "player-a",
			effectType:  "damage",
			policies:    map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess: true,
		},
		{
			name:        "DamageNPC_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner: "master",
			effectType:  "damage",
			policies:    map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess: true,
		},
		{
			name:        "HealDifferentPlayer_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner: "player-b",
			effectType:  "heal",
			policies:    map[string]policy.CampaignPolicy{"campaign-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess: true,
		},
		{
			name:           "DamageDifferentPlayer_NoPolicyProviderConfigured_DefaultsToPveOnly_Blocked",
			targetOwner:    "player-b",
			effectType:     "damage",
			policies:       nil,
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
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
					Actor:   &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
				// dmApplyEffect fetches post-effect status (characterStatusAfter)
				// on any successful ApplyEffect call, regardless of this
				// subtest's expected PvP outcome — must be configured even
				// for cases the gate is expected to block, since a fake
				// GetCharacterStatus response left nil (unlike a real gRPC
				// client, which never returns (nil, nil)) would panic.
				getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
			}
			fakeLLM := &fakeLLMProvider{
				responses: []llm.CompletionResponse{
					{Text: "Kestrel acts."},
					{ToolCalls: []llm.ToolCall{{
						ID: "call_1", Name: "apply_effect",
						Arguments: json.RawMessage(fmt.Sprintf(`{"character_id":"target-char","effect_type":%q,"amount":5}`, tt.effectType)),
					}}},
					{Text: "The dust settles."},
				},
			}

			var ts *httptest.Server
			var st *store.SQLiteEventStore
			if tt.policies != nil {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			} else {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
			}
			defer ts.Close()
			seedCharacter(t, st, "char-a", "campaign-pvp", "player-a")
			seedCharacter(t, st, "target-char", "campaign-pvp", tt.targetOwner)

			conn := dialAndJoin(t, ts, "campaign-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-pvp", "player-a", "char-a", "I attack!"); err != nil {
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
			if tt.wantSuccess && fakeEngine.lastApplyEffectRequest == nil {
				t.Error("ApplyEffect was never called, want it called since this case should succeed")
			}
			if !tt.wantSuccess && fakeEngine.lastApplyEffectRequest != nil {
				t.Error("ApplyEffect was called, want the PvP gate to have blocked the call before it reached the system engine")
			}
		})
	}
}

// TestServe_NarrativePlayerInput_MaturityTierPrompt_InjectedIntoFastPassSystemPrompt
// covers design doc §9.5/§6.5: an operator-configured maturity-tier
// constraint is appended to the DM's system prompt — prompting-only, not
// a hard filter, per design doc's own description of this setting.
func TestServe_NarrativePlayerInput_MaturityTierPrompt_InjectedIntoFastPassSystemPrompt(t *testing.T) {
	const constraint = "Keep content suitable for all ages: no graphic violence, no sexual content."
	policies := map[string]policy.CampaignPolicy{
		"campaign-maturity": {PvPPolicy: policy.PvPPolicyPveOnly, MaturityTierPrompt: constraint},
	}
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Kestrel draws a sword."}}
	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil, policy.NewJSONFileProvider(policies))
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-maturity", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-maturity", "player-a", "char-a", "I draw my sword."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	fastPassCall := fakeLLM.callAt(t, 0)
	if !strings.Contains(fastPassCall.SystemPrompt, constraint) {
		t.Errorf("fast pass SystemPrompt = %q, want it to contain the configured maturity constraint %q", fastPassCall.SystemPrompt, constraint)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_MaturityTierPrompt_InjectedIntoSlowPassSystemPrompt
// is the slow-pass equivalent — runSlowPass uses Messages, not
// SystemPrompt, so the constraint must appear in the first (system)
// message instead.
func TestServe_NarrativePlayerInput_SlowPass_MaturityTierPrompt_InjectedIntoSlowPassSystemPrompt(t *testing.T) {
	const constraint = "Keep content suitable for all ages: no graphic violence, no sexual content."
	policies := map[string]policy.CampaignPolicy{
		"campaign-maturity-slow": {PvPPolicy: policy.PvPPolicyPveOnly, MaturityTierPrompt: constraint},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel draws a sword."},
			{Text: "The blade catches the torchlight."},
		},
	}
	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil, policy.NewJSONFileProvider(policies))
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-maturity-slow", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-maturity-slow", "player-a", "char-a", "I draw my sword."); err != nil {
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
	if len(slowPassCall.Messages) == 0 || slowPassCall.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("slow pass call Messages[0] = %+v, want a RoleSystem message first", slowPassCall.Messages)
	}
	if !strings.Contains(slowPassCall.Messages[0].Content, constraint) {
		t.Errorf("slow pass system message = %q, want it to contain the configured maturity constraint %q", slowPassCall.Messages[0].Content, constraint)
	}
}
