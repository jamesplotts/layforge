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
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// attackToolCallLLM builds a fakeLLMProvider that narrates, calls
// toolName (melee_attack or ranged_attack) with the given arguments, then
// narrates the outcome — the same three-response shape
// castSpellToolCallLLM uses for cast_spell.
func attackToolCallLLM(toolName, argsJSON string) *fakeLLMProvider {
	return &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel closes in, weapon drawn."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: toolName,
				Arguments: json.RawMessage(argsJSON),
			}}},
			{Text: "Steel rings against steel."},
		},
	}
}

// TestServe_NarrativePlayerInput_SlowPass_MeleeAttack_Success_PersistsAttackerAndTarget
// covers the happy path of the hard gate melee_attack/ranged_attack add
// on top of apply_effect (CLAUDE.md's "gates over prompting"): a
// successful Attack RPC response must be persisted for both the attacker
// (action economy spent) and the damaged target, not just echoed back in
// the tool result — same shape as CastSpell's own persistence test.
func TestServe_NarrativePlayerInput_SlowPass_MeleeAttack_Success_PersistsAttackerAndTarget(t *testing.T) {
	attackerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "actionEconomy": map[string]any{"hasAction": false}})
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
			ResultMessage: "Kestrel's longsword connects for 8 slashing damage.",
			Attacker:      &systemenginepb.Actor{ActorId: "attacker-char", CharacterData: attackerData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
			TargetDamaged: true,
		},
	}
	fakeLLM := attackToolCallLLM("melee_attack", `{"character_id":"attacker-char","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "attacker-char", "campaign-attack", "player-a")
	seedCharacter(t, st, "target-char", "campaign-attack", "master")

	conn := dialAndJoin(t, ts, "campaign-attack", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-attack", "player-a", "attacker-char", "I swing my sword at it!"); err != nil {
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
	if toolResult.Payload.ToolName != "melee_attack" {
		t.Errorf("tool.result ToolName = %q, want %q", toolResult.Payload.ToolName, "melee_attack")
	}

	if fakeEngine.lastAttackRequest == nil {
		t.Fatal("Attack was never called")
	}
	if fakeEngine.lastAttackRequest.Kind != systemenginepb.AttackKind_ATTACK_KIND_MELEE {
		t.Errorf("Attack called with Kind = %v, want ATTACK_KIND_MELEE", fakeEngine.lastAttackRequest.Kind)
	}
	if fakeEngine.lastAttackRequest.GridContext != nil {
		t.Errorf("Attack called with GridContext = %+v, want nil (no combat map exists for this campaign)", fakeEngine.lastAttackRequest.GridContext)
	}

	savedAttacker, err := st.GetCharacter(ctx, "attacker-char")
	if err != nil {
		t.Fatalf("GetCharacter(attacker-char) error = %v", err)
	}
	var savedAttackerData map[string]any
	if err := json.Unmarshal(savedAttacker.CharacterData, &savedAttackerData); err != nil {
		t.Fatalf("unmarshaling saved attacker data error = %v", err)
	}
	economy, _ := savedAttackerData["actionEconomy"].(map[string]any)
	if economy["hasAction"] != false {
		t.Errorf("persisted attacker actionEconomy.hasAction = %v, want false (the engine's post-attack action economy)", economy["hasAction"])
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
		t.Errorf("persisted target hitPoints.current = %v, want 4 (the engine's post-attack damage)", hp["current"])
	}
}

// TestServe_NarrativePlayerInput_SlowPass_RangedAttack_EngineRejects_ReturnsFailureToolResult
// is the actual proof this is a hard code-level gate and not just good
// narration: when the engine rejects the attack (wrong weapon kind
// equipped, no weapon equipped, etc.), the tool.result must report
// failure and nothing must be persisted — same shape as CastSpell's own
// rejection test.
func TestServe_NarrativePlayerInput_SlowPass_RangedAttack_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		attackResp: &systemenginepb.AttackResponse{
			Success: false,
			Error:   "Longsword cannot be used for a ranged attack — it has neither Thrown nor Ammunition.",
		},
	}
	fakeLLM := attackToolCallLLM("ranged_attack", `{"character_id":"attacker-char","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "attacker-char", "campaign-attack-reject", "player-a")
	seedCharacter(t, st, "target-char", "campaign-attack-reject", "master")

	conn := dialAndJoin(t, ts, "campaign-attack-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-attack-reject", "player-a", "attacker-char", "I fire my bow— wait, I mean I shoot my sword at it!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the attack)")
	}
	if toolResult.Payload.ReasonCode != "attack_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "attack_failed")
	}

	saved, err := st.GetCharacter(ctx, "attacker-char")
	if err != nil {
		t.Fatalf("GetCharacter(attacker-char) error = %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(saved.CharacterData, &data); err != nil {
		t.Fatalf("unmarshaling saved attacker data error = %v", err)
	}
	if data["name"] != "Kestrel" {
		t.Errorf("attacker CharacterData changed after a rejected attack: %v", data)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_Attack_PvPGate mirrors
// CastSpell_PvPGate's matrix exactly (design doc §9.1) for Attack's own
// identically-shaped post-hoc gate: since Master can't know in advance
// whether an attack will connect, the gate is applied based on
// AttackResponse.TargetDamaged after calling the engine, not before. The
// attacker's own mutation (action economy spent) must still persist even
// when the gate blocks the target's mutation, since the attack genuinely
// happened.
func TestServe_NarrativePlayerInput_SlowPass_Attack_PvPGate(t *testing.T) {
	tests := []struct {
		name           string
		targetOwner    string // "player-a" (acting player's own character), "player-b" (a different player), "master" (an NPC)
		targetDamaged  bool
		policies       map[string]policy.CampaignPolicy // nil = no provider configured at all
		wantSuccess    bool
		wantReasonCode string
	}{
		{
			name:           "DamageDifferentPlayer_PveOnly_Blocked",
			targetOwner:    "player-b",
			targetDamaged:  true,
			policies:       map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
		{
			name:          "DamageDifferentPlayer_Allowed_Succeeds",
			targetOwner:   "player-b",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:   true,
		},
		{
			name:          "DamageDifferentPlayer_WithConsent_ConsentGranted_Succeeds",
			targetOwner:   "player-b",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyWithConsent, PvPConsent: []string{"player-b"}}},
			wantSuccess:   true,
		},
		{
			name:           "DamageDifferentPlayer_WithConsent_NoConsent_Blocked",
			targetOwner:    "player-b",
			targetDamaged:  true,
			policies:       map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyWithConsent}},
			wantSuccess:    false,
			wantReasonCode: "pvp_no_consent",
		},
		{
			name:          "DamageOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "player-a",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:   true,
		},
		{
			name:          "DamageNPC_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "master",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:   true,
		},
		{
			name:          "MissDoesNotDamage_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "player-b",
			targetDamaged: false,
			policies:      map[string]policy.CampaignPolicy{"campaign-attack-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:   true,
		},
		{
			name:           "DamageDifferentPlayer_NoPolicyProviderConfigured_DefaultsToPveOnly_Blocked",
			targetOwner:    "player-b",
			targetDamaged:  true,
			policies:       nil,
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attackerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "actionEconomy": map[string]any{"hasAction": false}})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				attackResp: &systemenginepb.AttackResponse{
					Success:       true,
					Hit:           tt.targetDamaged,
					ResultMessage: "The attack resolves.",
					Attacker:      &systemenginepb.Actor{ActorId: "attacker-char", CharacterData: attackerData, SchemaVersion: "opencombatengine-v1"},
					Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
					TargetDamaged: tt.targetDamaged,
				},
			}
			fakeLLM := attackToolCallLLM("melee_attack", `{"character_id":"attacker-char","target_character_id":"target-char"}`)

			var ts *httptest.Server
			var st *store.SQLiteEventStore
			if tt.policies != nil {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			} else {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
			}
			defer ts.Close()
			seedCharacter(t, st, "attacker-char", "campaign-attack-pvp", "player-a")
			seedCharacter(t, st, "target-char", "campaign-attack-pvp", tt.targetOwner)

			conn := dialAndJoin(t, ts, "campaign-attack-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-attack-pvp", "player-a", "attacker-char", "I swing my sword at it!"); err != nil {
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

			// Regardless of the PvP outcome, the attacker's own mutation
			// (action economy spent) must always persist — only the
			// target's mutation is conditional.
			savedAttacker, err := st.GetCharacter(ctx, "attacker-char")
			if err != nil {
				t.Fatalf("GetCharacter(attacker-char) error = %v", err)
			}
			var savedAttackerData map[string]any
			if err := json.Unmarshal(savedAttacker.CharacterData, &savedAttackerData); err != nil {
				t.Fatalf("unmarshaling saved attacker data error = %v", err)
			}
			economy, _ := savedAttackerData["actionEconomy"].(map[string]any)
			if economy["hasAction"] != false {
				t.Errorf("persisted attacker actionEconomy.hasAction = %v, want false (attacker mutation must persist even when the PvP gate blocks the target)", economy["hasAction"])
			}
		})
	}
}

// TestServe_NarrativePlayerInput_SlowPass_MeleeAttack_WithCombatMap_PopulatesGridContext
// covers wiring internal/combatmap's real positions/blocking cells into
// AttackRequest.grid_context, mirroring
// CastSpell_WithCombatMap_PopulatesGridContext exactly but for
// melee_attack — buildGridContext (combat_map.go) is fully generic and
// reused unchanged from the cast_spell work.
func TestServe_NarrativePlayerInput_SlowPass_MeleeAttack_WithCombatMap_PopulatesGridContext(t *testing.T) {
	attackerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{Total: 10, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 10, Label: "d20"}}},
			}, nil
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          noOpStartTurnResp,
		attackResp: &systemenginepb.AttackResponse{
			Success:       true,
			Hit:           true,
			ResultMessage: "Kestrel's blade finds its mark.",
			Attacker:      &systemenginepb.Actor{ActorId: "char-a", CharacterData: attackerData, SchemaVersion: "opencombatengine-v1"},
			TargetDamaged: true,
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel squares off against the goblin."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "start_combat", Arguments: json.RawMessage(`{"character_ids":["char-a","char-b"]}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "generate_combat_map", Arguments: json.RawMessage(`{}`)}}},
			{ToolCalls: []llm.ToolCall{{ID: "call_3", Name: "melee_attack", Arguments: json.RawMessage(`{"character_id":"char-a","target_character_id":"char-b"}`)}}},
			{Text: "Steel meets flesh."},
		},
	}
	campaignID := "campaign-attack-with-map"
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacterWithData(t, st, "char-a", campaignID, "player-a", `{"name":"Kestrel","team":"party","combatStats":{"speed":30}}`)
	seedCharacterWithData(t, st, "char-b", campaignID, "player-b", `{"name":"Grum","team":"monsters","combatStats":{"speed":30}}`)

	conn := dialAndJoin(t, ts, campaignID, "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, campaignID, "player-a", "char-a", "I fight the goblin, then swing my sword at it!"); err != nil {
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

	if fakeEngine.lastAttackRequest == nil {
		t.Fatal("Attack was never called")
	}
	gc := fakeEngine.lastAttackRequest.GridContext
	if gc == nil {
		t.Fatal("Attack called with GridContext = nil, want it populated (a combat map with both char-a and char-b placed exists)")
	}
	if gc.CasterPosition == nil || gc.TargetPosition == nil {
		t.Fatalf("GridContext = %+v, want non-nil CasterPosition and TargetPosition", gc)
	}
}
