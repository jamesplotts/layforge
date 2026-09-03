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

func TestServe_NarrativePlayerInput_SlowPass_EquipItem_Success_Persists(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		equipItemResp: &systemenginepb.EquipItemResponse{
			Success:       true,
			ResultMessage: "Kestrel equips Longsword.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("equip_item", `{"character_id":"actor-char","item_name":"Longsword","slot":"main_hand"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-equip", "player-a")

	conn := dialAndJoin(t, ts, "campaign-equip", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-equip", "player-a", "actor-char", "I ready my longsword!"); err != nil {
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
	if fakeEngine.lastEquipItemRequest == nil {
		t.Fatal("EquipItem was never called")
	}
	if fakeEngine.lastEquipItemRequest.Slot != systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_MAIN_HAND {
		t.Errorf("EquipItem called with Slot = %v, want EQUIPMENT_SLOT_MAIN_HAND", fakeEngine.lastEquipItemRequest.Slot)
	}
	if fakeEngine.lastEquipItemRequest.ItemName != "Longsword" {
		t.Errorf("EquipItem called with ItemName = %q, want %q", fakeEngine.lastEquipItemRequest.ItemName, "Longsword")
	}

	if _, err := st.GetCharacter(ctx, "actor-char"); err != nil {
		t.Fatalf("GetCharacter(actor-char) error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_EquipItem_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		equipItemResp: &systemenginepb.EquipItemResponse{
			Success: false,
			Error:   "Longsword is not in Kestrel's inventory.",
		},
	}
	fakeLLM := toolCallLLM("equip_item", `{"character_id":"actor-char","item_name":"Longsword","slot":"main_hand"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-equip-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-equip-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-equip-reject", "player-a", "actor-char", "I ready my longsword!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the equip)")
	}
	if toolResult.Payload.ReasonCode != "equip_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "equip_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_EquipItem_InvalidSlot_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("equip_item", `{"character_id":"actor-char","item_name":"Longsword","slot":"tail"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-equip-invalid", "player-a")

	conn := dialAndJoin(t, ts, "campaign-equip-invalid", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-equip-invalid", "player-a", "actor-char", "I ready my tail!"); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (invalid slot)")
	}
	if fakeEngine.lastEquipItemRequest != nil {
		t.Error("EquipItem was called despite an invalid slot argument — should have been rejected before calling the engine")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_UnequipItem_Success_Persists(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		unequipItemResp: &systemenginepb.UnequipItemResponse{
			Success:       true,
			ResultMessage: "Kestrel unequips MainHand.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("unequip_item", `{"character_id":"actor-char","slot":"main_hand"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-unequip", "player-a")

	conn := dialAndJoin(t, ts, "campaign-unequip", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-unequip", "player-a", "actor-char", "I sheathe my weapon."); err != nil {
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
	if fakeEngine.lastUnequipItemRequest == nil {
		t.Fatal("UnequipItem was never called")
	}
	if fakeEngine.lastUnequipItemRequest.Slot != systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_MAIN_HAND {
		t.Errorf("UnequipItem called with Slot = %v, want EQUIPMENT_SLOT_MAIN_HAND", fakeEngine.lastUnequipItemRequest.Slot)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_UnequipItem_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		unequipItemResp: &systemenginepb.UnequipItemResponse{
			Success: false,
			Error:   "MainHand is already empty.",
		},
	}
	fakeLLM := toolCallLLM("unequip_item", `{"character_id":"actor-char","slot":"main_hand"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-unequip-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-unequip-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-unequip-reject", "player-a", "actor-char", "I sheathe my weapon."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the unequip)")
	}
	if toolResult.Payload.ReasonCode != "unequip_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "unequip_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ReceiveItem_Success_Persists(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		addItemToInventoryResp: &systemenginepb.AddItemToInventoryResponse{
			Success:       true,
			ResultMessage: "Kestrel receives Potion of Healing.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("receive_item", `{"character_id":"actor-char","item_name":"Potion of Healing"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-receive", "player-a")

	conn := dialAndJoin(t, ts, "campaign-receive", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-receive", "player-a", "actor-char", "I pick up the vial."); err != nil {
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
	if fakeEngine.lastAddItemToInventoryRequest == nil {
		t.Fatal("AddItemToInventory was never called")
	}
	if fakeEngine.lastAddItemToInventoryRequest.ItemName != "Potion of Healing" {
		t.Errorf("AddItemToInventory called with ItemName = %q, want %q", fakeEngine.lastAddItemToInventoryRequest.ItemName, "Potion of Healing")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ReceiveItem_UnrecognizedItem_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		addItemToInventoryResp: &systemenginepb.AddItemToInventoryResponse{
			Success: false,
			Error:   "'Vorpal Whatchamacallit' is not a recognized item.",
		},
	}
	fakeLLM := toolCallLLM("receive_item", `{"character_id":"actor-char","item_name":"Vorpal Whatchamacallit"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-receive-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-receive-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-receive-reject", "player-a", "actor-char", "I pick up the strange device."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (unrecognized item name)")
	}
	if toolResult.Payload.ReasonCode != "receive_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "receive_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_DiscardItem_Success_Persists(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		removeItemFromInventoryResp: &systemenginepb.RemoveItemFromInventoryResponse{
			Success:       true,
			ResultMessage: "Kestrel discards Torch.",
			Actor:         &systemenginepb.Actor{ActorId: "actor-char", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("discard_item", `{"character_id":"actor-char","item_name":"Torch"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-discard", "player-a")

	conn := dialAndJoin(t, ts, "campaign-discard", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-discard", "player-a", "actor-char", "I drop the spent torch."); err != nil {
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
	if fakeEngine.lastRemoveItemFromInventoryRequest == nil {
		t.Fatal("RemoveItemFromInventory was never called")
	}
	if fakeEngine.lastRemoveItemFromInventoryRequest.ItemName != "Torch" {
		t.Errorf("RemoveItemFromInventory called with ItemName = %q, want %q", fakeEngine.lastRemoveItemFromInventoryRequest.ItemName, "Torch")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_DiscardItem_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		removeItemFromInventoryResp: &systemenginepb.RemoveItemFromInventoryResponse{
			Success: false,
			Error:   "Torch is not in Kestrel's inventory.",
		},
	}
	fakeLLM := toolCallLLM("discard_item", `{"character_id":"actor-char","item_name":"Torch"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-discard-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-discard-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-discard-reject", "player-a", "actor-char", "I drop the torch I don't have."); err != nil {
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
		t.Fatalf("tool.result Success = true, want false (the engine rejected the discard)")
	}
	if toolResult.Payload.ReasonCode != "discard_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "discard_failed")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GiveItem_Success_PersistsSourceAndTarget(t *testing.T) {
	sourceData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		transferItemResp: &systemenginepb.TransferItemResponse{
			Success:       true,
			ResultMessage: "Kestrel gives Torch to Target.",
			Source:        &systemenginepb.Actor{ActorId: "actor-char", CharacterData: sourceData, SchemaVersion: "opencombatengine-v1"},
			Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("give_item", `{"character_id":"actor-char","target_character_id":"target-char","item_name":"Torch"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-give", "player-a")
	seedCharacter(t, st, "target-char", "campaign-give", "master")

	conn := dialAndJoin(t, ts, "campaign-give", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-give", "player-a", "actor-char", "I hand over the torch."); err != nil {
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
	if fakeEngine.lastTransferItemRequest == nil {
		t.Fatal("TransferItem was never called")
	}
	if fakeEngine.lastTransferItemRequest.ItemName != "Torch" {
		t.Errorf("TransferItem called with ItemName = %q, want %q", fakeEngine.lastTransferItemRequest.ItemName, "Torch")
	}

	if _, err := st.GetCharacter(ctx, "actor-char"); err != nil {
		t.Fatalf("GetCharacter(actor-char) error = %v", err)
	}
	if _, err := st.GetCharacter(ctx, "target-char"); err != nil {
		t.Fatalf("GetCharacter(target-char) error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GiveItem_EngineRejects_ReturnsFailureToolResult(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		transferItemResp: &systemenginepb.TransferItemResponse{
			Success: false,
			Error:   "Torch is not in Kestrel's inventory.",
		},
	}
	fakeLLM := toolCallLLM("give_item", `{"character_id":"actor-char","target_character_id":"target-char","item_name":"Torch"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "actor-char", "campaign-give-reject", "player-a")
	seedCharacter(t, st, "target-char", "campaign-give-reject", "master")

	conn := dialAndJoin(t, ts, "campaign-give-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-give-reject", "player-a", "actor-char", "I hand over a torch I don't have."); err != nil {
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
	if toolResult.Payload.ReasonCode != "give_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "give_failed")
	}
}

// TestServe_NarrativePlayerInput_SlowPass_GiveItem_PvPGate mirrors
// Grapple/Shove's own PvP-gate matrix, but keyed on the GIVING
// character's owner relative to the acting player (actingSenderID),
// not the recipient's — dmGiveItem's gate guards against a different
// player's character having an item taken away from it, never against
// handing one TO a different player's character, which is never
// hostile. Unlike Grapple/Shove the gate here runs before the engine is
// ever called (dmGiveItem's own doc comment explains why), so a blocked
// case never reaches TransferItem at all.
func TestServe_NarrativePlayerInput_SlowPass_GiveItem_PvPGate(t *testing.T) {
	tests := []struct {
		name             string
		sourceOwner      string
		policies         map[string]policy.CampaignPolicy
		wantSuccess      bool
		wantReasonCode   string
		wantEngineCalled bool
	}{
		{
			name:             "GiveOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			sourceOwner:      "player-a",
			policies:         map[string]policy.CampaignPolicy{"campaign-give-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "GiveDifferentPlayersCharacter_PveOnly_Blocked",
			sourceOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-give-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      false,
			wantReasonCode:   "pvp_blocked",
			wantEngineCalled: false,
		},
		{
			name:             "GiveDifferentPlayersCharacter_Allowed_Succeeds",
			sourceOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-give-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "GiveNPCsItem_NotGated_SucceedsEvenUnderPveOnly",
			sourceOwner:      "master",
			policies:         map[string]policy.CampaignPolicy{"campaign-give-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			targetData, err := structpb.NewStruct(map[string]any{"name": "Target"})
			if err != nil {
				t.Fatalf("structpb.NewStruct() error = %v", err)
			}
			fakeEngine := &fakeSystemEngineClient{
				transferItemResp: &systemenginepb.TransferItemResponse{
					Success:       true,
					ResultMessage: "The attempt resolves.",
					Source:        &systemenginepb.Actor{ActorId: "actor-char", CharacterData: sourceData, SchemaVersion: "opencombatengine-v1"},
					Target:        &systemenginepb.Actor{ActorId: "target-char", CharacterData: targetData, SchemaVersion: "opencombatengine-v1"},
				},
			}
			fakeLLM := toolCallLLM("give_item", `{"character_id":"actor-char","target_character_id":"target-char","item_name":"Torch"}`)

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			defer ts.Close()
			seedCharacter(t, st, "actor-char", "campaign-give-pvp", tt.sourceOwner)
			seedCharacter(t, st, "target-char", "campaign-give-pvp", "player-b")

			conn := dialAndJoin(t, ts, "campaign-give-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-give-pvp", "player-a", "actor-char", "I hand it over."); err != nil {
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
			if (fakeEngine.lastTransferItemRequest != nil) != tt.wantEngineCalled {
				t.Errorf("TransferItem called = %v, want %v", fakeEngine.lastTransferItemRequest != nil, tt.wantEngineCalled)
			}
		})
	}
}

