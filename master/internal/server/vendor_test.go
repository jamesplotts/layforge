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

func TestServe_NarrativePlayerInput_SlowPass_CheckItemPrice_Success_QueriesEngineForRealItem(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		getItemInfoResp: &systemenginepb.GetItemInfoResponse{
			Success: true, ItemName: "Longsword", Gold: 15,
		},
	}
	fakeLLM := toolCallLLM("check_item_price", `{"item_name":"Longsword"}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-price", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-price", "player-a", "player-a-char", "How much for a longsword?"); err != nil {
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
	if fakeEngine.lastGetItemInfoRequest == nil || fakeEngine.lastGetItemInfoRequest.ItemName != "Longsword" {
		t.Errorf("GetItemInfo called with ItemName = %+v, want %q", fakeEngine.lastGetItemInfoRequest, "Longsword")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_CheckItemPrice_UnrecognizedItem_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		getItemInfoResp: &systemenginepb.GetItemInfoResponse{
			Success: false, Error: "'Sword of Nonsense' is not a recognized item.",
		},
	}
	fakeLLM := toolCallLLM("check_item_price", `{"item_name":"Sword of Nonsense"}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-price-bad", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-price-bad", "player-a", "player-a-char", "How much for a made-up sword?"); err != nil {
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
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "item_not_found" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "item_not_found")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ListVendorInventory_Success_QueriesEngineForEachItem(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		listInventoryResp: &systemenginepb.ListInventoryResponse{Success: true, ItemNames: []string{"Torch"}},
		getItemInfoResp:   &systemenginepb.GetItemInfoResponse{Success: true, ItemName: "Torch", Copper: 1},
	}
	fakeLLM := toolCallLLM("list_vendor_inventory", `{"character_id":"vendor-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "vendor-char", "campaign-list-inv", "master")

	conn := dialAndJoin(t, ts, "campaign-list-inv", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-inv", "player-a", "player-a-char", "What does the shop have?"); err != nil {
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
	if fakeEngine.lastListInventoryRequest == nil || fakeEngine.lastListInventoryRequest.Actor.ActorId != "vendor-char" {
		t.Errorf("ListInventory called with Actor = %+v, want vendor-char", fakeEngine.lastListInventoryRequest)
	}
	if fakeEngine.lastGetItemInfoRequest == nil || fakeEngine.lastGetItemInfoRequest.ItemName != "Torch" {
		t.Errorf("GetItemInfo called with ItemName = %+v, want %q", fakeEngine.lastGetItemInfoRequest, "Torch")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ListVendorInventory_EngineRejects_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		listInventoryResp: &systemenginepb.ListInventoryResponse{Success: false, Error: "unable to resolve actor"},
	}
	fakeLLM := toolCallLLM("list_vendor_inventory", `{"character_id":"vendor-char"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "vendor-char", "campaign-list-inv-bad", "master")

	conn := dialAndJoin(t, ts, "campaign-list-inv-bad", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-inv-bad", "player-a", "player-a-char", "What does the shop have?"); err != nil {
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
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "list_inventory_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "list_inventory_failed")
	}
}

// vendorSellFixtureEngine builds a fakeSystemEngineClient wired for a
// successful vendor_sell_item call: GetItemInfo returns a base price of
// 10 gold (1000 copper); TransferItem and TransferCurrency both succeed.
func vendorSellFixtureEngine(t *testing.T) *fakeSystemEngineClient {
	t.Helper()
	vendorData, err := structpb.NewStruct(map[string]any{"name": "Shopkeep"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	buyerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	return &fakeSystemEngineClient{
		getItemInfoResp: &systemenginepb.GetItemInfoResponse{Success: true, ItemName: "Longsword", Gold: 10},
		transferItemResp: &systemenginepb.TransferItemResponse{
			Success: true,
			Source:  &systemenginepb.Actor{ActorId: "vendor-char", CharacterData: vendorData, SchemaVersion: "opencombatengine-v1"},
			Target:  &systemenginepb.Actor{ActorId: "buyer-char", CharacterData: buyerData, SchemaVersion: "opencombatengine-v1"},
		},
		transferCurrencyResp: &systemenginepb.TransferCurrencyResponse{
			Success: true,
			Source:  &systemenginepb.Actor{ActorId: "buyer-char", CharacterData: buyerData, SchemaVersion: "opencombatengine-v1"},
			Target:  &systemenginepb.Actor{ActorId: "vendor-char", CharacterData: vendorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorSellItem_Success_ChargesMultiplierAdjustedPrice(t *testing.T) {
	fakeEngine := vendorSellFixtureEngine(t)
	fakeLLM := toolCallLLM("vendor_sell_item", `{"vendor_character_id":"vendor-char","buyer_character_id":"buyer-char","item_name":"Longsword"}`)

	policies := map[string]policy.CampaignPolicy{"campaign-vendor-sell": {PriceMultiplier: 1.5}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()
	seedCharacter(t, st, "vendor-char", "campaign-vendor-sell", "master")
	seedCharacter(t, st, "buyer-char", "campaign-vendor-sell", "player-a")

	conn := dialAndJoin(t, ts, "campaign-vendor-sell", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-sell", "player-a", "buyer-char", "I buy the longsword."); err != nil {
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
	if fakeEngine.lastTransferItemRequest.Source.ActorId != "vendor-char" || fakeEngine.lastTransferItemRequest.Target.ActorId != "buyer-char" {
		t.Errorf("TransferItem Source/Target = %s/%s, want vendor-char/buyer-char", fakeEngine.lastTransferItemRequest.Source.ActorId, fakeEngine.lastTransferItemRequest.Target.ActorId)
	}

	if fakeEngine.lastTransferCurrencyRequest == nil {
		t.Fatal("TransferCurrency was never called")
	}
	// Base price 10 gold (1000 copper) * 1.5 multiplier = 1500 copper,
	// which the minimal-coin decomposition renders as 1 platinum + 5
	// gold (not "15 gold") — proving the campaign's price_multiplier was
	// actually applied, not the raw engine value.
	req := fakeEngine.lastTransferCurrencyRequest
	if req.Platinum != 1 || req.Gold != 5 || req.Silver != 0 || req.Copper != 0 {
		t.Errorf("TransferCurrency denominations = %d pp, %d gp, %d sp, %d cp, want 1 pp, 5 gp, 0 sp, 0 cp (1500 copper total)",
			req.Platinum, req.Gold, req.Silver, req.Copper)
	}
	if fakeEngine.lastTransferCurrencyRequest.Source.ActorId != "buyer-char" || fakeEngine.lastTransferCurrencyRequest.Target.ActorId != "vendor-char" {
		t.Errorf("TransferCurrency Source/Target = %s/%s, want buyer-char/vendor-char", fakeEngine.lastTransferCurrencyRequest.Source.ActorId, fakeEngine.lastTransferCurrencyRequest.Target.ActorId)
	}

	if _, err := st.GetCharacter(ctx, "buyer-char"); err != nil {
		t.Errorf("GetCharacter(buyer-char) error = %v", err)
	}
	if _, err := st.GetCharacter(ctx, "vendor-char"); err != nil {
		t.Errorf("GetCharacter(vendor-char) error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorSellItem_VendorDoesNotStockItem_CurrencyLegNeverAttempted(t *testing.T) {
	fakeEngine := vendorSellFixtureEngine(t)
	fakeEngine.transferItemResp = &systemenginepb.TransferItemResponse{Success: false, Error: "Longsword is not in Shopkeep's inventory."}
	fakeLLM := toolCallLLM("vendor_sell_item", `{"vendor_character_id":"vendor-char","buyer_character_id":"buyer-char","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "vendor-char", "campaign-vendor-sell-nostock", "master")
	seedCharacter(t, st, "buyer-char", "campaign-vendor-sell-nostock", "player-a")

	conn := dialAndJoin(t, ts, "campaign-vendor-sell-nostock", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-sell-nostock", "player-a", "buyer-char", "I buy the longsword."); err != nil {
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
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "vendor_sell_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "vendor_sell_failed")
	}
	if fakeEngine.lastTransferCurrencyRequest != nil {
		t.Error("TransferCurrency was called, want it never attempted when the item leg fails")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorSellItem_BuyerCannotAfford_NothingPersisted(t *testing.T) {
	fakeEngine := vendorSellFixtureEngine(t)
	fakeEngine.transferCurrencyResp = &systemenginepb.TransferCurrencyResponse{Success: false, Error: "Insufficient gold."}
	fakeLLM := toolCallLLM("vendor_sell_item", `{"vendor_character_id":"vendor-char","buyer_character_id":"buyer-char","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	const vendorOriginal = `{"name":"Shopkeep original"}`
	const buyerOriginal = `{"name":"Kestrel original"}`
	seedCharacterWithData(t, st, "vendor-char", "campaign-vendor-sell-poor", "master", vendorOriginal)
	seedCharacterWithData(t, st, "buyer-char", "campaign-vendor-sell-poor", "player-a", buyerOriginal)

	conn := dialAndJoin(t, ts, "campaign-vendor-sell-poor", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-sell-poor", "player-a", "buyer-char", "I buy the longsword."); err != nil {
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
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "insufficient_funds" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "insufficient_funds")
	}

	vendor, err := st.GetCharacter(ctx, "vendor-char")
	if err != nil {
		t.Fatalf("GetCharacter(vendor-char) error = %v", err)
	}
	if string(vendor.CharacterData) != vendorOriginal {
		t.Errorf("vendor-char CharacterData = %s, want unchanged %s (a failed second leg must persist nothing)", vendor.CharacterData, vendorOriginal)
	}
	buyer, err := st.GetCharacter(ctx, "buyer-char")
	if err != nil {
		t.Fatalf("GetCharacter(buyer-char) error = %v", err)
	}
	if string(buyer.CharacterData) != buyerOriginal {
		t.Errorf("buyer-char CharacterData = %s, want unchanged %s (a failed second leg must persist nothing)", buyer.CharacterData, buyerOriginal)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorSellItem_PvPGate(t *testing.T) {
	tests := []struct {
		name             string
		vendorOwner      string
		vendorDead       bool
		policies         map[string]policy.CampaignPolicy
		wantSuccess      bool
		wantReasonCode   string
		wantEngineCalled bool
	}{
		{
			name:             "VendorIsNPC_NotGated_SucceedsEvenUnderPveOnly",
			vendorOwner:      "master",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-sell-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "VendorOwnedByDifferentPlayer_PveOnly_Blocked",
			vendorOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-sell-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      false,
			wantReasonCode:   "pvp_blocked",
			wantEngineCalled: false,
		},
		{
			name:             "VendorOwnedByDifferentPlayer_Allowed_Succeeds",
			vendorOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-sell-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "DeadVendorOwnedByDifferentPlayer_NotGated_SucceedsEvenUnderPveOnly",
			vendorOwner:      "player-b",
			vendorDead:       true,
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-sell-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeEngine := vendorSellFixtureEngine(t)
			characterStatus := systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE
			if tt.vendorDead {
				characterStatus = systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD
			}
			fakeEngine.getCharacterStatusResp = &systemenginepb.GetCharacterStatusResponse{Status: characterStatus}
			fakeLLM := toolCallLLM("vendor_sell_item", `{"vendor_character_id":"vendor-char","buyer_character_id":"buyer-char","item_name":"Longsword"}`)

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			defer ts.Close()
			seedCharacter(t, st, "vendor-char", "campaign-vendor-sell-pvp", tt.vendorOwner)
			seedCharacter(t, st, "buyer-char", "campaign-vendor-sell-pvp", "player-a")

			conn := dialAndJoin(t, ts, "campaign-vendor-sell-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-sell-pvp", "player-a", "buyer-char", "I buy the longsword."); err != nil {
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

// vendorBuyFixtureEngine builds a fakeSystemEngineClient wired for a
// successful vendor_buy_item call: GetItemInfo returns a base price of
// 10 gold (1000 copper); TransferItem and TransferCurrency both succeed.
func vendorBuyFixtureEngine(t *testing.T) *fakeSystemEngineClient {
	t.Helper()
	sellerData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	vendorData, err := structpb.NewStruct(map[string]any{"name": "Shopkeep"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	return &fakeSystemEngineClient{
		getItemInfoResp: &systemenginepb.GetItemInfoResponse{Success: true, ItemName: "Longsword", Gold: 10},
		transferItemResp: &systemenginepb.TransferItemResponse{
			Success: true,
			Source:  &systemenginepb.Actor{ActorId: "seller-char", CharacterData: sellerData, SchemaVersion: "opencombatengine-v1"},
			Target:  &systemenginepb.Actor{ActorId: "vendor-char", CharacterData: vendorData, SchemaVersion: "opencombatengine-v1"},
		},
		transferCurrencyResp: &systemenginepb.TransferCurrencyResponse{
			Success: true,
			Source:  &systemenginepb.Actor{ActorId: "vendor-char", CharacterData: vendorData, SchemaVersion: "opencombatengine-v1"},
			Target:  &systemenginepb.Actor{ActorId: "seller-char", CharacterData: sellerData, SchemaVersion: "opencombatengine-v1"},
		},
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorBuyItem_Success_PaysMultiplierAdjustedPrice(t *testing.T) {
	fakeEngine := vendorBuyFixtureEngine(t)
	fakeLLM := toolCallLLM("vendor_buy_item", `{"vendor_character_id":"vendor-char","seller_character_id":"seller-char","item_name":"Longsword"}`)

	policies := map[string]policy.CampaignPolicy{"campaign-vendor-buy": {PriceMultiplier: 0.5}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()
	seedCharacter(t, st, "vendor-char", "campaign-vendor-buy", "master")
	seedCharacter(t, st, "seller-char", "campaign-vendor-buy", "player-a")

	conn := dialAndJoin(t, ts, "campaign-vendor-buy", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-buy", "player-a", "seller-char", "I sell the longsword."); err != nil {
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
	if fakeEngine.lastTransferItemRequest.Source.ActorId != "seller-char" || fakeEngine.lastTransferItemRequest.Target.ActorId != "vendor-char" {
		t.Errorf("TransferItem Source/Target = %s/%s, want seller-char/vendor-char", fakeEngine.lastTransferItemRequest.Source.ActorId, fakeEngine.lastTransferItemRequest.Target.ActorId)
	}

	if fakeEngine.lastTransferCurrencyRequest == nil {
		t.Fatal("TransferCurrency was never called")
	}
	// Base price 10 gold (1000 copper) * 0.5 multiplier = 5 gold (500
	// copper).
	if fakeEngine.lastTransferCurrencyRequest.Gold != 5 {
		t.Errorf("TransferCurrency Gold = %d, want 5 (10 base * 0.5 multiplier)", fakeEngine.lastTransferCurrencyRequest.Gold)
	}
	if fakeEngine.lastTransferCurrencyRequest.Source.ActorId != "vendor-char" || fakeEngine.lastTransferCurrencyRequest.Target.ActorId != "seller-char" {
		t.Errorf("TransferCurrency Source/Target = %s/%s, want vendor-char/seller-char", fakeEngine.lastTransferCurrencyRequest.Source.ActorId, fakeEngine.lastTransferCurrencyRequest.Target.ActorId)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorBuyItem_VendorCannotAfford_NothingPersisted(t *testing.T) {
	fakeEngine := vendorBuyFixtureEngine(t)
	fakeEngine.transferCurrencyResp = &systemenginepb.TransferCurrencyResponse{Success: false, Error: "Insufficient gold."}
	fakeLLM := toolCallLLM("vendor_buy_item", `{"vendor_character_id":"vendor-char","seller_character_id":"seller-char","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	const vendorOriginal = `{"name":"Shopkeep original"}`
	const sellerOriginal = `{"name":"Kestrel original"}`
	seedCharacterWithData(t, st, "vendor-char", "campaign-vendor-buy-poor", "master", vendorOriginal)
	seedCharacterWithData(t, st, "seller-char", "campaign-vendor-buy-poor", "player-a", sellerOriginal)

	conn := dialAndJoin(t, ts, "campaign-vendor-buy-poor", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-buy-poor", "player-a", "seller-char", "I sell the longsword."); err != nil {
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
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "vendor_insufficient_funds" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "vendor_insufficient_funds")
	}

	vendor, err := st.GetCharacter(ctx, "vendor-char")
	if err != nil {
		t.Fatalf("GetCharacter(vendor-char) error = %v", err)
	}
	if string(vendor.CharacterData) != vendorOriginal {
		t.Errorf("vendor-char CharacterData = %s, want unchanged %s (a failed second leg must persist nothing)", vendor.CharacterData, vendorOriginal)
	}
	seller, err := st.GetCharacter(ctx, "seller-char")
	if err != nil {
		t.Fatalf("GetCharacter(seller-char) error = %v", err)
	}
	if string(seller.CharacterData) != sellerOriginal {
		t.Errorf("seller-char CharacterData = %s, want unchanged %s (a failed second leg must persist nothing)", seller.CharacterData, sellerOriginal)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_VendorBuyItem_SellerPvPGate(t *testing.T) {
	tests := []struct {
		name             string
		sellerOwner      string
		policies         map[string]policy.CampaignPolicy
		wantSuccess      bool
		wantReasonCode   string
		wantEngineCalled bool
	}{
		{
			name:             "SellerIsActingPlayersOwnCharacter_NotGated_SucceedsEvenUnderPveOnly",
			sellerOwner:      "player-a",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-buy-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
		{
			name:             "SellerOwnedByDifferentPlayer_PveOnly_Blocked",
			sellerOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-buy-pvp": {PvPPolicy: policy.PvPPolicyPveOnly}},
			wantSuccess:      false,
			wantReasonCode:   "pvp_blocked",
			wantEngineCalled: false,
		},
		{
			name:             "SellerOwnedByDifferentPlayer_Allowed_Succeeds",
			sellerOwner:      "player-b",
			policies:         map[string]policy.CampaignPolicy{"campaign-vendor-buy-pvp": {PvPPolicy: policy.PvPPolicyAllowed}},
			wantSuccess:      true,
			wantEngineCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeEngine := vendorBuyFixtureEngine(t)
			fakeEngine.getCharacterStatusResp = &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE}
			fakeLLM := toolCallLLM("vendor_buy_item", `{"vendor_character_id":"vendor-char","seller_character_id":"seller-char","item_name":"Longsword"}`)

			ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(tt.policies))
			defer ts.Close()
			seedCharacter(t, st, "vendor-char", "campaign-vendor-buy-pvp", "master")
			seedCharacter(t, st, "seller-char", "campaign-vendor-buy-pvp", tt.sellerOwner)

			conn := dialAndJoin(t, ts, "campaign-vendor-buy-pvp", "player-a")
			defer conn.CloseNow()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := sendPlayerInput(ctx, conn, "campaign-vendor-buy-pvp", "player-a", "seller-char", "I sell the longsword."); err != nil {
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
