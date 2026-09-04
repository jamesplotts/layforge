// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// sableRavinePackDir is the real, committed example pack — see
// campaignpack's own loader_test.go for the same fixture.
const sableRavinePackDir = "../../../campaign-packs/sable-ravine"

// bindPack binds the real sable-ravine pack to campaignID directly via
// the store, the same way internal/admin's pack-binding endpoint will
// (this test file predates that endpoint's own tests, and doesn't need
// to go through it to exercise the DM tools built on top).
func bindPack(t *testing.T, st *store.SQLiteEventStore, campaignID string) {
	t.Helper()
	if err := st.SaveCampaignPack(context.Background(), campaignID, sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ListLocations_Success_ListsRealLocations(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_locations", `{}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-list-locations")

	conn := dialAndJoin(t, ts, "campaign-list-locations", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-locations", "player-a", "char-a", "Where can we go?"); err != nil {
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
}

func TestServe_NarrativePlayerInput_SlowPass_ListLocations_NoPackBound_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_locations", `{}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-no-pack", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-no-pack", "player-a", "char-a", "Where can we go?"); err != nil {
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
	if toolResult.Payload.ReasonCode != "no_campaign_pack" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "no_campaign_pack")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TravelTo_Bootstrap_SucceedsToAnyRealLocation(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("travel_to", `{"location_id":"keep-stonewatch"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-travel-bootstrap")

	conn := dialAndJoin(t, ts, "campaign-travel-bootstrap", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-travel-bootstrap", "player-a", "char-a", "We head to the keep."); err != nil {
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

	loc, err := st.GetPartyLocation(ctx, "campaign-travel-bootstrap")
	if err != nil {
		t.Fatalf("GetPartyLocation() error = %v", err)
	}
	if loc != "keep-stonewatch" {
		t.Errorf("GetPartyLocation() = %q, want %q", loc, "keep-stonewatch")
	}
	state, ok, err := st.GetLocationState(ctx, "campaign-travel-bootstrap", "keep-stonewatch")
	if err != nil {
		t.Fatalf("GetLocationState() error = %v", err)
	}
	if !ok || !state.Discovered {
		t.Errorf("GetLocationState(keep-stonewatch) = %+v, ok=%v, want Discovered=true", state, ok)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TravelTo_ConnectedLocation_Succeeds(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("travel_to", `{"location_id":"old-road"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-travel-connected")
	// keep-stonewatch connects to old-road (see campaign-packs/sable-ravine/locations/keep-stonewatch.md).
	if err := st.SetPartyLocation(context.Background(), "campaign-travel-connected", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-travel-connected", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-travel-connected", "player-a", "char-a", "We take the old road."); err != nil {
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
}

func TestServe_NarrativePlayerInput_SlowPass_TravelTo_NotConnected_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	// keep-stonewatch does NOT connect directly to ruined-shrine.
	fakeLLM := toolCallLLM("travel_to", `{"location_id":"ruined-shrine"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-travel-illegal")
	if err := st.SetPartyLocation(context.Background(), "campaign-travel-illegal", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-travel-illegal", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-travel-illegal", "player-a", "char-a", "We teleport to the shrine."); err != nil {
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
	if toolResult.Payload.ReasonCode != "not_reachable" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "not_reachable")
	}

	loc, err := st.GetPartyLocation(ctx, "campaign-travel-illegal")
	if err != nil || loc != "keep-stonewatch" {
		t.Errorf("GetPartyLocation() = %q, %v, want unchanged keep-stonewatch, nil", loc, err)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TravelTo_UnknownLocation_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("travel_to", `{"location_id":"nowhere-real"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-travel-unknown")

	conn := dialAndJoin(t, ts, "campaign-travel-unknown", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-travel-unknown", "player-a", "char-a", "We go nowhere real."); err != nil {
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
	if toolResult.Payload.ReasonCode != "location_not_found" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "location_not_found")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_StashItem_NoPartyLocation_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("stash_item", `{"character_id":"char-a","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-stash-nolocation")
	seedCharacter(t, st, "char-a", "campaign-stash-nolocation", "player-a")

	conn := dialAndJoin(t, ts, "campaign-stash-nolocation", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-stash-nolocation", "player-a", "char-a", "I stash my sword."); err != nil {
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
	if toolResult.Payload.ReasonCode != "no_party_location" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "no_party_location")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_StashItem_Success_RemovesFromInventoryAndRecordsStash(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		removeItemFromInventoryResp: &systemenginepb.RemoveItemFromInventoryResponse{
			Success: true, Actor: &systemenginepb.Actor{ActorId: "char-a", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("stash_item", `{"character_id":"char-a","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-stash-roundtrip")
	seedCharacter(t, st, "char-a", "campaign-stash-roundtrip", "player-a")
	if err := st.SetPartyLocation(context.Background(), "campaign-stash-roundtrip", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-stash-roundtrip", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-stash-roundtrip", "player-a", "char-a", "I stash my sword."); err != nil {
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
		t.Fatalf("stash tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}
	if fakeEngine.lastRemoveItemFromInventoryRequest == nil {
		t.Fatal("RemoveItemFromInventory was never called")
	}

	items, err := st.ListStashedItems(ctx, "campaign-stash-roundtrip", "keep-stonewatch")
	if err != nil {
		t.Fatalf("ListStashedItems() error = %v", err)
	}
	if len(items) != 1 || items[0].ItemName != "Longsword" {
		t.Fatalf("ListStashedItems() = %+v, want one stashed Longsword", items)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_RetrieveItem_Success_RemovesStashAndAddsToInventory(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		addItemToInventoryResp: &systemenginepb.AddItemToInventoryResponse{
			Success: true, Actor: &systemenginepb.Actor{ActorId: "char-a", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("retrieve_item", `{"character_id":"char-a","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-retrieve")
	seedCharacter(t, st, "char-a", "campaign-retrieve", "player-a")
	ctx0 := context.Background()
	if err := st.SetPartyLocation(ctx0, "campaign-retrieve", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}
	if err := st.StashItem(ctx0, "stash-1", "campaign-retrieve", "keep-stonewatch", "char-a", "Longsword"); err != nil {
		t.Fatalf("StashItem() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-retrieve", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-retrieve", "player-a", "char-a", "I grab my sword back."); err != nil {
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

	items, err := st.ListStashedItems(ctx, "campaign-retrieve", "keep-stonewatch")
	if err != nil {
		t.Fatalf("ListStashedItems() error = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("ListStashedItems() after retrieve = %+v, want empty", items)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_RetrieveItem_WrongLocation_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("retrieve_item", `{"character_id":"char-a","item_name":"Longsword"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-retrieve-wrong")
	seedCharacter(t, st, "char-a", "campaign-retrieve-wrong", "player-a")
	ctx0 := context.Background()
	if err := st.StashItem(ctx0, "stash-1", "campaign-retrieve-wrong", "keep-stonewatch", "char-a", "Longsword"); err != nil {
		t.Fatalf("StashItem() error = %v", err)
	}
	// Party is at old-road, not keep-stonewatch where the item is stashed.
	if err := st.SetPartyLocation(ctx0, "campaign-retrieve-wrong", "old-road"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-retrieve-wrong", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-retrieve-wrong", "player-a", "char-a", "I grab my sword back."); err != nil {
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
	if toolResult.Payload.ReasonCode != "nothing_stashed_here" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "nothing_stashed_here")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_StashCurrency_Success_RemovesFromCharacterAndAccumulatesStash(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		removeCurrencyResp: &systemenginepb.RemoveCurrencyResponse{
			Success: true, Actor: &systemenginepb.Actor{ActorId: "char-a", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("stash_currency", `{"character_id":"char-a","gold":10}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-stash-currency")
	seedCharacter(t, st, "char-a", "campaign-stash-currency", "player-a")
	if err := st.SetPartyLocation(context.Background(), "campaign-stash-currency", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-stash-currency", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-stash-currency", "player-a", "char-a", "I stash 10 gold."); err != nil {
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
	if fakeEngine.lastRemoveCurrencyRequest == nil || fakeEngine.lastRemoveCurrencyRequest.Gold != 10 {
		t.Errorf("RemoveCurrency called with = %+v, want Gold=10", fakeEngine.lastRemoveCurrencyRequest)
	}

	_, _, gold, _, err := st.GetStashedCurrency(ctx, "campaign-stash-currency", "keep-stonewatch", "char-a")
	if err != nil {
		t.Fatalf("GetStashedCurrency() error = %v", err)
	}
	if gold != 10 {
		t.Errorf("GetStashedCurrency() gold = %d, want 10", gold)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_RetrieveCurrency_InsufficientStashed_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("retrieve_currency", `{"character_id":"char-a","gold":10}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-retrieve-currency-poor")
	seedCharacter(t, st, "char-a", "campaign-retrieve-currency-poor", "player-a")
	ctx0 := context.Background()
	if err := st.SetPartyLocation(ctx0, "campaign-retrieve-currency-poor", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}
	if err := st.AddStashedCurrency(ctx0, "campaign-retrieve-currency-poor", "keep-stonewatch", "char-a", 0, 0, 5, 0); err != nil {
		t.Fatalf("AddStashedCurrency() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-retrieve-currency-poor", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-retrieve-currency-poor", "player-a", "char-a", "I take out 10 gold."); err != nil {
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
	if toolResult.Payload.ReasonCode != "insufficient_stashed_currency" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "insufficient_stashed_currency")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ClaimLocation_Success_PersistsClaim(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("claim_location", `{"note":"cleared and garrisoned"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-claim")
	if err := st.SetPartyLocation(context.Background(), "campaign-claim", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-claim", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-claim", "player-a", "char-a", "We claim this keep."); err != nil {
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

	state, ok, err := st.GetLocationState(ctx, "campaign-claim", "keep-stonewatch")
	if err != nil {
		t.Fatalf("GetLocationState() error = %v", err)
	}
	if !ok || !state.ClaimedByParty || state.ClaimNote != "cleared and garrisoned" {
		t.Errorf("GetLocationState() = %+v, ok=%v, want ClaimedByParty=true ClaimNote=%q", state, ok, "cleared and garrisoned")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_DMPromptIncludesCurrentLocationContext(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The keep looms ahead."}}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-location-context")
	if err := st.SetPartyLocation(context.Background(), "campaign-location-context", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-location-context", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-location-context", "player-a", "char-a", "I look around."); err != nil {
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

	fakeLLM.mu.Lock()
	defer fakeLLM.mu.Unlock()
	found := false
	for _, call := range fakeLLM.calls {
		for _, msg := range call.Messages {
			if strings.Contains(msg.Content, "Current location: keep-stonewatch") {
				found = true
			}
		}
	}
	if !found {
		t.Error("no LLM call received a \"Current location: keep-stonewatch\" context line")
	}
}
