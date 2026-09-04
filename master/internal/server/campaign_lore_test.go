// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
)

func TestServe_NarrativePlayerInput_SlowPass_ListNPCs_Success_ListsRealNPCs(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_npcs", `{}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-list-npcs")

	conn := dialAndJoin(t, ts, "campaign-list-npcs", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-npcs", "player-a", "char-a", "Who do we know of around here?"); err != nil {
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

func TestServe_NarrativePlayerInput_SlowPass_ListNPCs_NoPackBound_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_npcs", `{}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-list-npcs-no-pack", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-npcs-no-pack", "player-a", "char-a", "Who do we know of around here?"); err != nil {
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

func TestServe_NarrativePlayerInput_SlowPass_ListEncounters_Success_ListsRealEncounters(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_encounters", `{}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-list-encounters")

	conn := dialAndJoin(t, ts, "campaign-list-encounters", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-encounters", "player-a", "char-a", "What might we run into around here?"); err != nil {
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

func TestServe_NarrativePlayerInput_SlowPass_ListEncounters_NoPackBound_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_encounters", `{}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-list-encounters-no-pack", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-encounters-no-pack", "player-a", "char-a", "What might we run into around here?"); err != nil {
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
