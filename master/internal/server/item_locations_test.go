// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func TestServe_NarrativePlayerInput_SlowPass_ItemLocations_ListsRealLocations(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	fakeEngine := &fakeSystemEngineClient{
		listCarriedItemsResp: &systemenginepb.ListCarriedItemsResponse{
			Success: true,
			Items: []*systemenginepb.CarriedItem{
				{ItemName: "Crowbar", LocationDescription: "stowed in Explorer's Pack", Kind: systemenginepb.CarryLocationKind_CARRY_LOCATION_KIND_STOWED, ContainerName: "Explorer's Pack"},
				{ItemName: "Longsword", LocationDescription: "wielded (main hand)", Kind: systemenginepb.CarryLocationKind_CARRY_LOCATION_KIND_EQUIPPED, Slot: systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_MAIN_HAND},
			},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-item-locations", "player-a")

	conn := dialAndJoin(t, ts, "campaign-item-locations", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-item-locations", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if !strings.Contains(content, "Item locations:") {
		t.Fatalf("slow pass user content = %q, want an Item locations section", content)
	}
	if !strings.Contains(content, "- Crowbar: stowed in Explorer's Pack") {
		t.Errorf("slow pass user content = %q, want the Crowbar's real location listed", content)
	}
	if !strings.Contains(content, "- Longsword: wielded (main hand)") {
		t.Errorf("slow pass user content = %q, want the Longsword's real location listed", content)
	}
	if fakeEngine.lastListCarriedItemsRequest == nil {
		t.Fatal("ListCarriedItems was never called")
	}
	if fakeEngine.lastListCarriedItemsRequest.Actor.ActorId != "char-a" {
		t.Errorf("ListCarriedItems called for actor %q, want %q", fakeEngine.lastListCarriedItemsRequest.Actor.ActorId, "char-a")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ItemLocations_NoItemsCarried_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	fakeEngine := &fakeSystemEngineClient{
		listCarriedItemsResp: &systemenginepb.ListCarriedItemsResponse{Success: true},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-item-locations-empty", "player-a")

	conn := dialAndJoin(t, ts, "campaign-item-locations-empty", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-item-locations-empty", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Item locations") {
		t.Errorf("slow pass user content = %q, want no Item locations section for a character carrying nothing", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ItemLocations_EngineRejects_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	fakeEngine := &fakeSystemEngineClient{
		listCarriedItemsResp: &systemenginepb.ListCarriedItemsResponse{Success: false, Error: "malformed character data"},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-item-locations-reject", "player-a")

	conn := dialAndJoin(t, ts, "campaign-item-locations-reject", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-item-locations-reject", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Item locations") {
		t.Errorf("slow pass user content = %q, want no Item locations section when the engine call itself fails", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ItemLocations_NoSystemEngine_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-item-locations-noengine", "player-a")

	conn := dialAndJoin(t, ts, "campaign-item-locations-noengine", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-item-locations-noengine", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Item locations") {
		t.Errorf("slow pass user content = %q, want no Item locations section with no system engine configured", content)
	}
}
