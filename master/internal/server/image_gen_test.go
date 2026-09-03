// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/imagegen"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// fakeImageGenProvider is a minimal imagegen.Provider for testing the
// generate_scene_image DM tool's wiring (dm_tools.go) without a real
// ComfyUI instance — package imagegen's own tests cover the real HTTP
// client logic against a fake ComfyUI server; this fake is one level up,
// proving Server calls the provider correctly and handles its result/
// error, not re-testing ComfyUI's wire format.
type fakeImageGenProvider struct {
	imageURL string
	err      error
	// lastPrompt/lastMaturityTierPrompt capture the most recent call's
	// arguments, for asserting on what Server actually passed through.
	lastPrompt             string
	lastMaturityTierPrompt string
}

var _ imagegen.Provider = (*fakeImageGenProvider)(nil)

func (f *fakeImageGenProvider) GenerateSceneImage(_ context.Context, prompt, maturityTierPrompt string) (string, error) {
	f.lastPrompt = prompt
	f.lastMaturityTierPrompt = maturityTierPrompt
	if f.err != nil {
		return "", f.err
	}
	return f.imageURL, nil
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateSceneImage_Succeeds_BroadcastsSceneImage(t *testing.T) {
	fakeImg := &fakeImageGenProvider{imageURL: "http://localhost:8188/view?filename=scene.png"}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party enters a moonlit clearing."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "generate_scene_image",
				Arguments: json.RawMessage(`{"prompt":"a moonlit forest clearing with ancient standing stones"}`),
			}}},
			{Text: "The clearing glows faintly under the moon."},
		},
	}
	ts, _ := newTestServerWithLLMSystemEngineAndImageGen(t, fakeLLM, nil, fakeImg)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-image", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-image", "player-a", "char-a", "We enter the clearing."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	// dmGenerateSceneImage broadcasts narrative.scene_image itself,
	// before returning to callDMTool — runSlowPass's own
	// broadcastToolResult only runs after callDMTool returns, so the
	// real wire order is scene_image, then tool.result (not the other
	// way around).
	var sceneImage protocol.NarrativeSceneImageMessage
	if err := wsjson.Read(ctx, conn, &sceneImage); err != nil {
		t.Fatalf("Read(narrative.scene_image) error = %v", err)
	}
	if sceneImage.Payload.ImageURL != fakeImg.imageURL {
		t.Errorf("narrative.scene_image ImageURL = %q, want %q", sceneImage.Payload.ImageURL, fakeImg.imageURL)
	}
	if fakeImg.lastPrompt != "a moonlit forest clearing with ancient standing stones" {
		t.Errorf("provider was called with prompt %q, want the model's exact prompt", fakeImg.lastPrompt)
	}

	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.ToolName != "generate_scene_image" || !toolResult.Payload.Success {
		t.Fatalf("tool.result = %+v, want a successful generate_scene_image", toolResult.Payload)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateSceneImage_ProviderError_ReturnsFailureNoBroadcast(t *testing.T) {
	fakeImg := &fakeImageGenProvider{err: errors.New("ComfyUI unreachable")}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party enters a ruined temple."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "generate_scene_image",
				Arguments: json.RawMessage(`{"prompt":"a ruined temple overgrown with vines"}`),
			}}},
			{Text: "The temple looms in the dark."},
		},
	}
	ts, _ := newTestServerWithLLMSystemEngineAndImageGen(t, fakeLLM, nil, fakeImg)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-image-fail", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-image-fail", "player-a", "char-a", "We enter the temple."); err != nil {
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
		t.Fatal("tool.result Success = true, want false (the provider returned an error)")
	}
	if toolResult.Payload.ReasonCode != "image_gen_failed" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "image_gen_failed")
	}

	// No narrative.scene_image should follow a failed generation — the
	// next message should be the final narration instead.
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}
	if prose.Payload.Text != "The temple looms in the dark." {
		t.Errorf("narrative.dm_prose Text = %q, want the final narration (no scene_image should have been broadcast in between)", prose.Payload.Text)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GenerateSceneImage_MaturityTierPassedThrough(t *testing.T) {
	fakeImg := &fakeImageGenProvider{imageURL: "http://localhost:8188/view?filename=scene.png"}
	policies := map[string]policy.CampaignPolicy{
		"campaign-image-tier": {PvPPolicy: policy.PvPPolicyPveOnly, ImageMaturityTierPrompt: "no graphic violence"},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The party enters a battlefield."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "generate_scene_image",
				Arguments: json.RawMessage(`{"prompt":"the aftermath of a battle"}`),
			}}},
			{Text: "The battlefield is quiet now."},
		},
	}
	// This test needs both a policy provider and an image-gen provider,
	// which neither newTestServerWithLLMAndSystemEngine nor
	// newTestServerWithLLMSystemEngineAndImageGen supports on its own —
	// see newTestServerWithLLMSystemEngineImageGenAndPolicy.
	ts, _ := newTestServerWithLLMSystemEngineImageGenAndPolicy(t, fakeLLM, nil, fakeImg, policy.NewJSONFileProvider(policies))
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-image-tier", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-image-tier", "player-a", "char-a", "We survey the field."); err != nil {
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

	if fakeImg.lastMaturityTierPrompt != "no graphic violence" {
		t.Errorf("provider was called with maturityTierPrompt %q, want the campaign's configured image tier", fakeImg.lastMaturityTierPrompt)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ImageGenConfigured_ToolIsOffered(t *testing.T) {
	fakeImg := &fakeImageGenProvider{imageURL: "http://localhost:8188/view?filename=scene.png"}
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Kestrel looks around."}}
	ts, _ := newTestServerWithLLMSystemEngineAndImageGen(t, fakeLLM, nil, fakeImg)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-image-offered", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-image-offered", "player-a", "char-a", "I look around."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	// Wait for the slow pass to actually finish (runSlowPass runs in its
	// own goroutine) before asserting on fakeLLM's recorded calls —
	// otherwise this races the goroutine and is flaky depending on
	// exactly how much synchronous work runSlowPass does before its own
	// Complete() call.
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	found := false
	for _, tool := range slowPassCall.Tools {
		if tool.Name == "generate_scene_image" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("slow pass Tools = %+v, want generate_scene_image offered when an imagegen.Provider is configured", slowPassCall.Tools)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_NoImageGenProvider_ToolNotOffered(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Kestrel looks around."}}
	ts, _ := newTestServerWithLLMSystemEngineAndImageGen(t, fakeLLM, nil, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-image-not-offered", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-image-not-offered", "player-a", "char-a", "I look around."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	// See the sibling ImageGenConfigured test for why this read (and not
	// asserting on fakeLLM immediately after the bubble) is required.
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	for _, tool := range slowPassCall.Tools {
		if tool.Name == "generate_scene_image" {
			t.Errorf("slow pass Tools includes generate_scene_image, want it absent when no imagegen.Provider is configured")
		}
	}
}
