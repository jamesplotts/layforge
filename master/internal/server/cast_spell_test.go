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

// castSpellToolCallLLM builds a fakeLLMProvider that narrates, calls
// cast_spell with the given arguments, then narrates the outcome — the
// same three-response shape pvp_policy_test.go's ApplyEffect_PvPGate
// matrix uses for apply_effect.
func castSpellToolCallLLM(argsJSON string) *fakeLLMProvider {
	return &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel gestures and speaks a word of power."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "cast_spell",
				Arguments: json.RawMessage(argsJSON),
			}}},
			{Text: "Motes of light streak across the room."},
		},
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CastSpell_Success_PersistsCasterAndTarget
// covers the happy path of the hard gate against casting unprepared
// spells (CLAUDE.md's "gates over prompting"): a successful CastSpell RPC
// response must be persisted for both the caster (slot consumed) and the
// damaged target, not just echoed back in the tool result.
func TestServe_NarrativePlayerInput_SlowPass_CastSpell_Success_PersistsCasterAndTarget(t *testing.T) {
	casterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "spellcasting": map[string]any{"slotsRemaining": 1.0}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	targetData, err := structpb.NewStruct(map[string]any{"name": "Target", "hitPoints": map[string]any{"current": 4.0, "max": 20.0}})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		castSpellResp: &systemenginepb.CastSpellResponse{
			Success:       true,
			ResultMessage: "Magic Missile strikes true for 10 force damage.",
			Caster:        &systemenginepb.Actor{ActorId: "caster-char", CharacterData: casterData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
			TargetDamaged: true,
		},
	}
	fakeLLM := castSpellToolCallLLM(`{"character_id":"caster-char","spell_name":"Magic Missile","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "caster-char", "campaign-cast", "player-a")
	seedCharacter(t, st, "target-char", "campaign-cast", "master")

	conn := dialAndJoin(t, ts, "campaign-cast", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-cast", "player-a", "caster-char", "I cast Magic Missile at it!"); err != nil {
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
	if toolResult.Payload.ToolName != "cast_spell" {
		t.Errorf("tool.result ToolName = %q, want %q", toolResult.Payload.ToolName, "cast_spell")
	}

	if fakeEngine.lastCastSpellRequest == nil {
		t.Fatal("CastSpell was never called")
	}
	if fakeEngine.lastCastSpellRequest.SpellName != "Magic Missile" {
		t.Errorf("CastSpell called with SpellName = %q, want %q", fakeEngine.lastCastSpellRequest.SpellName, "Magic Missile")
	}

	savedCaster, err := st.GetCharacter(ctx, "caster-char")
	if err != nil {
		t.Fatalf("GetCharacter(caster-char) error = %v", err)
	}
	var savedCasterData map[string]any
	if err := json.Unmarshal(savedCaster.CharacterData, &savedCasterData); err != nil {
		t.Fatalf("unmarshaling saved caster data error = %v", err)
	}
	spellcasting, _ := savedCasterData["spellcasting"].(map[string]any)
	if spellcasting["slotsRemaining"] != 1.0 {
		t.Errorf("persisted caster spellcasting.slotsRemaining = %v, want 1 (the engine's post-cast slot count)", spellcasting["slotsRemaining"])
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
		t.Errorf("persisted target hitPoints.current = %v, want 4 (the engine's post-cast damage)", hp["current"])
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CastSpell_EngineRejects_ReturnsFailureToolResult
// is the actual proof this is a hard code-level gate and not just good
// narration: when the engine rejects the cast (spell not prepared, no
// slot, etc.), the tool.result must report failure and nothing must be
// persisted.
func TestServe_NarrativePlayerInput_SlowPass_CastSpell_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		castSpellResp: &systemenginepb.CastSpellResponse{
			Success: false,
			Error:   "spell 'Fireball' is not prepared",
		},
	}
	fakeLLM := castSpellToolCallLLM(`{"character_id":"caster-char","spell_name":"Fireball","target_character_id":"target-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "caster-char", "campaign-cast-reject", "player-a")
	seedCharacter(t, st, "target-char", "campaign-cast-reject", "master")

	conn := dialAndJoin(t, ts, "campaign-cast-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-cast-reject", "player-a", "caster-char", "I cast Fireball at it!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the cast)")
	}
	if toolResult.Payload.ReasonCode != "cast_spell_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "cast_spell_failed")
	}

	saved, err := st.GetCharacter(ctx, "caster-char")
	if err != nil {
		t.Fatalf("GetCharacter(caster-char) error = %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(saved.CharacterData, &data); err != nil {
		t.Fatalf("unmarshaling saved caster data error = %v", err)
	}
	if data["name"] != "Kestrel" {
		t.Errorf("caster CharacterData changed after a rejected cast: %v", data)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CastSpell_PvPGate mirrors
// ApplyEffect_PvPGate's matrix exactly (design doc §9.1), but for
// cast_spell's differently-timed gate: since Master can't know in
// advance whether a named spell deals damage, the gate is applied post
// hoc based on CastSpellResponse.TargetDamaged (see dmCastSpell's doc
// comment in dm_tools.go) rather than before calling the engine. The
// caster's own mutation (slot consumed) must still persist even when the
// gate blocks the target's mutation, since the cast genuinely happened.
func TestServe_NarrativePlayerInput_SlowPass_CastSpell_PvPGate(t *testing.T) {
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
			policies:       map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:    false,
			wantReasonCode: "pvp_blocked",
		},
		{
			name:          "DamageDifferentPlayer_Allowed_Succeeds",
			targetOwner:   "player-b",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:   true,
		},
		{
			name:          "DamageDifferentPlayer_WithConsent_ConsentGranted_Succeeds",
			targetOwner:   "player-b",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyWithConsent, PvPConsent: []string{"player-b"}}},
			wantSuccess:   true,
		},
		{
			name:           "DamageDifferentPlayer_WithConsent_NoConsent_Blocked",
			targetOwner:    "player-b",
			targetDamaged:  true,
			policies:       map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyWithConsent}},
			wantSuccess:    false,
			wantReasonCode: "pvp_no_consent",
		},
		{
			name:          "DamageOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "player-a",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:   true,
		},
		{
			name:          "DamageNPC_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "master",
			targetDamaged: true,
			policies:      map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:   true,
		},
		{
			name:          "NonDamagingSpellOnDifferentPlayer_NotGated_SucceedsEvenUnderPveOnly",
			targetOwner:   "player-b",
			targetDamaged: false,
			policies:      map[string]policy.CampaignPolicy{"campaign-cast-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
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
			casterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel", "spellcasting": map[string]any{"slotsRemaining": 1.0}})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				castSpellResp: &systemenginepb.CastSpellResponse{
					Success:       true,
					ResultMessage: "The spell takes effect.",
					Caster:        &systemenginepb.Actor{ActorId: "caster-char", CharacterData: casterData, SchemaVersion: "opencombatengine-v1"},
					Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
					TargetDamaged: tt.targetDamaged,
				},
			}
			fakeLLM := castSpellToolCallLLM(`{"character_id":"caster-char","spell_name":"Magic Missile","target_character_id":"target-char"}`)

			var ts *httptest.Server
			var st *store.SQLiteEventStore
			if tt.policies != nil {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			} else {
				ts, st = newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
			}
			defer ts.Close()
			seedCharacter(t, st, "caster-char", "campaign-cast-pvp", "player-a")
			seedCharacter(t, st, "target-char", "campaign-cast-pvp", tt.targetOwner)

			conn := dialAndJoin(t, ts, "campaign-cast-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-cast-pvp", "player-a", "caster-char", "I cast Magic Missile at it!"); err != nil {
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

			// Regardless of the PvP outcome, the caster's own mutation (the
			// spell was genuinely cast, the slot genuinely spent) must
			// always persist — only the target's mutation is conditional.
			savedCaster, err := st.GetCharacter(ctx, "caster-char")
			if err != nil {
				t.Fatalf("GetCharacter(caster-char) error = %v", err)
			}
			var savedCasterData map[string]any
			if err := json.Unmarshal(savedCaster.CharacterData, &savedCasterData); err != nil {
				t.Fatalf("unmarshaling saved caster data error = %v", err)
			}
			spellcasting, _ := savedCasterData["spellcasting"].(map[string]any)
			if spellcasting["slotsRemaining"] != 1.0 {
				t.Errorf("persisted caster spellcasting.slotsRemaining = %v, want 1 (caster mutation must persist even when the PvP gate blocks the target)", spellcasting["slotsRemaining"])
			}
		})
	}
}
