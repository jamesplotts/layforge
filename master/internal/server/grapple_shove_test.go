// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// toolCallLLM builds a fakeLLMProvider that narrates, calls toolName
// with the given arguments, then narrates the outcome — the same
// three-response shape attackToolCallLLM/castSpellToolCallLLM use.
func toolCallLLM(toolName, argsJSON string) *fakeLLMProvider {
	return &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel moves to act."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: toolName,
				Arguments: json.RawMessage(argsJSON),
			}}},
			{Text: "The attempt resolves."},
		},
	}
}

func TestServe_NarrativePlayerInput_SlowPass_OffhandAttack_Success_PersistsAttackerAndTarget(t *testing.T) {
	attackerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "actionEconomy": map[string]any{"hasBonusAction": false}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	targetData, err := structpb.NewStruct(map[string]any{"name": "Target", "hitPoints": map[string]any{"current": 4.0, "max": 20.0}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		attackResp: &systemenginepb.AttackResponse{
			Success:       true,
			Hit:           true,
			ResultMessage: "Kestrel's off-hand dagger connects.",
			Attacker:      &systemenginepb.Actor{ActorId: "attacker-char", CharacterData: attackerData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
			TargetDamaged: true,
		},
	}
	fakeLLM := toolCallLLM("offhand_attack", `{"character_id":"attacker-char","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "attacker-char", "campaign-offhand", "player-a")
	seedCharacter(t, st, "target-char", "campaign-offhand", "master")

	conn := dialAndJoin(t, ts, "campaign-offhand", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-offhand", "player-a", "attacker-char", "I follow up with my off-hand dagger!"); err != nil {
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

	if fakeEngine.lastAttackRequest == nil {
		t.Fatal("Attack was never called")
	}
	if fakeEngine.lastAttackRequest.Kind != systemenginepb.AttackKind_ATTACK_KIND_OFFHAND {
		t.Errorf("Attack called with Kind = %v, want ATTACK_KIND_OFFHAND", fakeEngine.lastAttackRequest.Kind)
	}

	savedTarget, err := st.GetCharacter(ctx, "target-char")
	if err != nil {
		t.Fatalf("GetCharacter(target-char) error = %v", err)
	}
	var savedTargetData map[string]any
	if err := json.Unmarshal(savedTarget.CharacterData, &savedTargetData); err != nil {
		t.Fatalf("unmarshaling saved target data error = %v", err)
	}
	hp, _ := savedTargetData["hitPoints"].(map[string]any)
	if hp["current"] != 4.0 {
		t.Errorf("persisted target hitPoints.current = %v, want 4", hp["current"])
	}
}

func TestServe_NarrativePlayerInput_SlowPass_Grapple_Success_PersistsActorAndTarget(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "actionEconomy": map[string]any{"hasAction": false}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	targetData, err := structpb.NewStruct(map[string]any{"name": "Target", "conditions": map[string]any{"conditions": []any{map[string]any{"name": "Grappled"}}}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		grappleResp: &systemenginepb.GrappleResponse{
			Success:       true,
			Grappled:      true,
			ResultMessage: "Kestrel seizes the target.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("grapple", `{"character_id":"actor-char","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-grapple", "player-a")
	seedCharacter(t, st, "target-char", "campaign-grapple", "master")

	conn := dialAndJoin(t, ts, "campaign-grapple", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-grapple", "player-a", "actor-char", "I grab it!"); err != nil {
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
	if toolResult.Payload.ToolName != "grapple" {
		t.Errorf("tool.result ToolName = %q, want %q", toolResult.Payload.ToolName, "grapple")
	}
	if fakeEngine.lastGrappleRequest == nil {
		t.Fatal("Grapple was never called")
	}

	savedTarget, err := st.GetCharacter(ctx, "target-char")
	if err != nil {
		t.Fatalf("GetCharacter(target-char) error = %v", err)
	}
	var savedTargetData map[string]any
	if err := json.Unmarshal(savedTarget.CharacterData, &savedTargetData); err != nil {
		t.Fatalf("unmarshaling saved target data error = %v", err)
	}
	if _, ok := savedTargetData["conditions"]; !ok {
		t.Errorf("persisted target data = %v, want the engine's applied Grappled condition", savedTargetData)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_Grapple_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		grappleResp: &systemenginepb.GrappleResponse{
			Success: false,
			Error:   "Kestrel has no hand free to grapple.",
		},
	}
	fakeLLM := toolCallLLM("grapple", `{"character_id":"actor-char","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-grapple-reject", "player-a")
	seedCharacter(t, st, "target-char", "campaign-grapple-reject", "master")

	conn := dialAndJoin(t, ts, "campaign-grapple-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-grapple-reject", "player-a", "actor-char", "I try to grab it!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the grapple)")
	}
	if toolResult.Payload.ReasonCode != "grapple_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "grapple_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_Shove_Success_SendsRealEffect(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		shoveResp: &systemenginepb.ShoveResponse{
			Success:       true,
			Shoved:        true,
			ResultMessage: "Kestrel shoves the target back.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("shove", `{"character_id":"actor-char","target_character_id":"target-char","effect":"push"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-shove", "player-a")
	seedCharacter(t, st, "target-char", "campaign-shove", "master")

	conn := dialAndJoin(t, ts, "campaign-shove", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-shove", "player-a", "actor-char", "I shove it back!"); err != nil {
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
	if fakeEngine.lastShoveRequest == nil {
		t.Fatal("Shove was never called")
	}
	if fakeEngine.lastShoveRequest.Effect != systemenginepb.ShoveEffect_SHOVE_EFFECT_PUSH {
		t.Errorf("Shove called with Effect = %v, want SHOVE_EFFECT_PUSH", fakeEngine.lastShoveRequest.Effect)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_Shove_InvalidEffect_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("shove", `{"character_id":"actor-char","target_character_id":"target-char","effect":"sideways"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-shove-invalid", "player-a")
	seedCharacter(t, st, "target-char", "campaign-shove-invalid", "master")

	conn := dialAndJoin(t, ts, "campaign-shove-invalid", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-shove-invalid", "player-a", "actor-char", "I shove it sideways!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (invalid effect)")
	}
	if fakeEngine.lastShoveRequest != nil {
		t.Error("Shove was called despite an invalid effect argument — should have been rejected before calling the engine")
	}
}

// TestServe_NarrativePlayerInput_SlowPass_Grapple_PvPGate mirrors
// Attack/CastSpell's own PvP-gate matrix, but keyed on
// GrappleResponse.Grappled instead of TargetDamaged/target_damaged —
// grapple never deals damage, but applying Grappled to a different
// player's character is the same kind of hostile mechanical effect the
// PvP policy is meant to gate.
func TestServe_NarrativePlayerInput_SlowPass_Grapple_PvPGate(t *testing.T) {
	tests := []struct {
		name           string
		targetOwner    string
		grappled       bool
		policies       map[string]policy.CampaignPolicy
		wantSuccess    bool
		wantReasonCode string
	}{
		{
			name:           "GrappleDifferentPlayer_PveOnly_Blocked",
			targetOwner:    "player-b",
			grappled:       true,
			policies:       map[string]policy.CampaignPolicy{"campaign-grapple-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
		{
			name:        "GrappleDifferentPlayer_Allowed_Succeeds",
			targetOwner: "player-b",
			grappled:    true,
			policies:    map[string]policy.CampaignPolicy{"campaign-grapple-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess: true,
		},
		{
			name:        "GrappleNPC_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner: "master",
			grappled:    true,
			policies:    map[string]policy.CampaignPolicy{"campaign-grapple-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess: true,
		},
		{
			name:        "FailedGrapple_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner: "player-b",
			grappled:    false,
			policies:    map[string]policy.CampaignPolicy{"campaign-grapple-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				grappleResp: &systemenginepb.GrappleResponse{
					Success:       true,
					Grappled:      tt.grappled,
					ResultMessage: "The attempt resolves.",
					Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
					Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
			}
			fakeLLM := toolCallLLM("grapple", `{"character_id":"actor-char","target_character_id":"target-char"}`)

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			defer ts.Close()
			seedCharacter(t, st, "actor-char", "campaign-grapple-pvp", "player-a")
			seedCharacter(t, st, "target-char", "campaign-grapple-pvp", tt.targetOwner)

			conn := dialAndJoin(t, ts, "campaign-grapple-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-grapple-pvp", "player-a", "actor-char", "I grab it!"); err != nil {
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
		})
	}
}
