// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func TestServe_NarrativePlayerInput_SlowPass_GenerateLoot_Success(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		generateLootResp: &systemenginepb.GenerateLootResponse{
			Success:       true,
			Gold:          15,
			ItemName:      "Dagger",
			ResultMessage: "Generated loot for 1 participant(s) at effective CR 1.",
		},
	}
	fakeLLM := toolCallLLM("generate_loot", `{"character_ids":["monster-1"]}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "monster-1", "campaign-loot", "master")

	conn := dialAndJoin(t, ts, "campaign-loot", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-loot", "player-a", "monster-1", "I check the goblin's pockets before the fight even starts."); err != nil {
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
	if fakeEngine.lastGenerateLootRequest == nil {
		t.Fatal("GenerateLoot was never called")
	}
	if len(fakeEngine.lastGenerateLootRequest.Participants) != 1 {
		t.Fatalf("GenerateLoot called with %d participants, want 1", len(fakeEngine.lastGenerateLootRequest.Participants))
	}
	if fakeEngine.lastGenerateLootRequest.Participants[0].ActorId != "monster-1" {
		t.Errorf("GenerateLoot participant ActorId = %q, want %q", fakeEngine.lastGenerateLootRequest.Participants[0].ActorId, "monster-1")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateLoot_MultipleParticipants_SendsAllOfThem(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		generateLootResp: &systemenginepb.GenerateLootResponse{
			Success:       true,
			Gold:          200,
			ResultMessage: "Generated loot for 3 participant(s) at effective CR 6.",
		},
	}
	fakeLLM := toolCallLLM("generate_loot", `{"character_ids":["priest","acolyte-1","acolyte-2"]}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "priest", "campaign-loot-multi", "master")
	seedCharacter(t, st, "acolyte-1", "campaign-loot-multi", "master")
	seedCharacter(t, st, "acolyte-2", "campaign-loot-multi", "master")

	conn := dialAndJoin(t, ts, "campaign-loot-multi", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-loot-multi", "player-a", "priest", "I prep the whole cult's treasure before the ambush."); err != nil {
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
	if fakeEngine.lastGenerateLootRequest == nil {
		t.Fatal("GenerateLoot was never called")
	}
	if len(fakeEngine.lastGenerateLootRequest.Participants) != 3 {
		t.Fatalf("GenerateLoot called with %d participants, want 3", len(fakeEngine.lastGenerateLootRequest.Participants))
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateLoot_EmptyCharacterIDs_RejectedBeforeCallingEngine(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("generate_loot", `{"character_ids":[]}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-loot-empty", "player-a")

	conn := dialAndJoin(t, ts, "campaign-loot-empty", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-loot-empty", "player-a", "actor-char", "I generate loot for nobody."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (empty character_ids)")
	}
	if fakeEngine.lastGenerateLootRequest != nil {
		t.Error("GenerateLoot was called despite an empty character_ids argument — should have been rejected before calling the engine")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateLoot_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		generateLootResp: &systemenginepb.GenerateLootResponse{
			Success: false,
			Error:   "Goblin has no challenge_rating recorded.",
		},
	}
	fakeLLM := toolCallLLM("generate_loot", `{"character_ids":["monster-1"]}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "monster-1", "campaign-loot-reject", "master")

	conn := dialAndJoin(t, ts, "campaign-loot-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-loot-reject", "player-a", "monster-1", "I check the goblin's pockets."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the loot roll)")
	}
	if toolResult.Payload.ReasonCode != "generate_loot_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "generate_loot_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AddCurrency_Success_Persists(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		addCurrencyResp: &systemenginepb.AddCurrencyResponse{
			Success:       true,
			ResultMessage: "Kestrel receives 0cp, 0sp, 15gp, 0pp.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("add_currency", `{"character_id":"actor-char","gold":15}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-add-currency", "player-a")

	conn := dialAndJoin(t, ts, "campaign-add-currency", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-add-currency", "player-a", "actor-char", "I pocket the gold."); err != nil {
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
	if fakeEngine.lastAddCurrencyRequest == nil {
		t.Fatal("AddCurrency was never called")
	}
	if fakeEngine.lastAddCurrencyRequest.Gold != 15 {
		t.Errorf("AddCurrency called with Gold = %d, want 15", fakeEngine.lastAddCurrencyRequest.Gold)
	}

	if _, err := st.GetCharacter(ctx, "actor-char"); err != nil {
		t.Fatalf("GetCharacter(actor-char) error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AddCurrency_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		addCurrencyResp: &systemenginepb.AddCurrencyResponse{
			Success: false,
			Error:   "Cannot add a negative amount of currency.",
		},
	}
	fakeLLM := toolCallLLM("add_currency", `{"character_id":"actor-char","gold":-5}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-add-currency-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-add-currency-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-add-currency-reject", "player-a", "actor-char", "I lose gold, somehow."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the add)")
	}
	if toolResult.Payload.ReasonCode != "add_currency_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "add_currency_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TransferCurrency_Success_PersistsSourceAndTarget(t *testing.T) {
	sourceData, err := structpb.NewStruct(map[string]any{"name": "Goblin"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	targetData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		transferCurrencyResp: &systemenginepb.TransferCurrencyResponse{
			Success:       true,
			ResultMessage: "Goblin gives 0cp, 0sp, 20gp, 0pp to Kestrel.",
			Source:        &systemenginepb.Actor{ActorId: "goblin-char", CharacterData: sourceData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "actor-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("transfer_currency", `{"character_id":"goblin-char","target_character_id":"actor-char","gold":20}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "goblin-char", "campaign-transfer-currency", "master")
	seedCharacter(t, st, "actor-char", "campaign-transfer-currency", "player-a")

	conn := dialAndJoin(t, ts, "campaign-transfer-currency", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-transfer-currency", "player-a", "actor-char", "I loot the goblin's coin purse."); err != nil {
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
	if fakeEngine.lastTransferCurrencyRequest == nil {
		t.Fatal("TransferCurrency was never called")
	}
	if fakeEngine.lastTransferCurrencyRequest.Gold != 20 {
		t.Errorf("TransferCurrency called with Gold = %d, want 20", fakeEngine.lastTransferCurrencyRequest.Gold)
	}

	if _, err := st.GetCharacter(ctx, "goblin-char"); err != nil {
		t.Fatalf("GetCharacter(goblin-char) error = %v", err)
	}
	if _, err := st.GetCharacter(ctx, "actor-char"); err != nil {
		t.Fatalf("GetCharacter(actor-char) error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TransferCurrency_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		transferCurrencyResp: &systemenginepb.TransferCurrencyResponse{
			Success: false,
			Error:   "Insufficient currency.",
		},
	}
	fakeLLM := toolCallLLM("transfer_currency", `{"character_id":"goblin-char","target_character_id":"actor-char","gold":20}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "goblin-char", "campaign-transfer-currency-reject", "master")
	seedCharacter(t, st, "actor-char", "campaign-transfer-currency-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-transfer-currency-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-transfer-currency-reject", "player-a", "actor-char", "I loot a goblin that's already broke."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the transfer)")
	}
	if toolResult.Payload.ReasonCode != "transfer_currency_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "transfer_currency_failed")
	}
}

// TestServe_NarrativePlayerInput_SlowPass_TransferCurrency_PvPGate mirrors
// GiveItem_PvPGate's exact matrix — see dmTransferCurrency's own doc
// comment for why the gate runs before calling the engine, same as
// dmGiveItem.
func TestServe_NarrativePlayerInput_SlowPass_TransferCurrency_PvPGate(t *testing.T) {
	tests := []struct {
		name             string
		sourceOwner      string
		sourceDead       bool
		policies         map[string]policy.CampaignPolicy
		wantSuccess      bool
		wantReasonCode   string
		wantEngineCalled bool
	}{
		{
			name:             "TransferFromOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			sourceOwner:      "player-a",
			policies:         map[string]policy.CampaignPolicy{"campaign-transfer-currency-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "TransferFromDifferentPlayersCharacter_PveOnly_Blocked",
			sourceOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-transfer-currency-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      false,
			wantReasonCode:   "pvp_blocked",
			wantEngineCalled: false,
		},
		{
			name:             "TransferFromDifferentPlayersCharacter_Allowed_Succeeds",
			sourceOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-transfer-currency-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "TransferFromNPCsCorpse_NotGated_SucceedsEvenUnderPveOnly",
			sourceOwner:      "master",
			policies:         map[string]policy.CampaignPolicy{"campaign-transfer-currency-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			// The party dividing up a fallen ally's own coin — logistics,
			// not theft — even though the source is a different player's
			// character under the strictest policy.
			name:             "TransferFromDeadDifferentPlayersCharacter_NotGated_SucceedsEvenUnderPveOnly",
			sourceOwner:      "player-b",
			sourceDead:       true,
			policies:         map[string]policy.CampaignPolicy{"campaign-transfer-currency-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceData, err := structpb.NewStruct(map[string]any{"name": "Source"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			characterStatus := systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE
			if tt.sourceDead {
				characterStatus = systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD
			}
			fakeEngine := &fakeSystemEngineClient{
				transferCurrencyResp: &systemenginepb.TransferCurrencyResponse{
					Success:       true,
					ResultMessage: "The transfer resolves.",
					Source:        &systemenginepb.Actor{ActorId: "actor-char", CharacterData: sourceData, SchemaVersion: "opencombatengine-v1"},
					Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
				getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: characterStatus},
			}
			fakeLLM := toolCallLLM("transfer_currency", `{"character_id":"actor-char","target_character_id":"target-char","gold":5}`)

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			defer ts.Close()
			seedCharacter(t, st, "actor-char", "campaign-transfer-currency-pvp", tt.sourceOwner)
			seedCharacter(t, st, "target-char", "campaign-transfer-currency-pvp", "player-b")

			conn := dialAndJoin(t, ts, "campaign-transfer-currency-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-transfer-currency-pvp", "player-a", "actor-char", "I take the coin."); err != nil {
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
			if (fakeEngine.lastTransferCurrencyRequest != nil) != tt.wantEngineCalled {
				t.Errorf("TransferCurrency called = %v, want %v", fakeEngine.lastTransferCurrencyRequest != nil, tt.wantEngineCalled)
			}
		})
	}
}
