// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// awaitReviewResult reads messages from conn until a
// character.review_result arrives (or the context expires) — the
// automatic review pass (character_review.go) runs in the background,
// so its outcome is a later, separate message after
// character.validation_result, not something a single conn.Read can
// assume is next.
func awaitReviewResult(ctx context.Context, conn *websocket.Conn) (protocol.CharacterReviewResultPayload, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return protocol.CharacterReviewResultPayload{}, err
		}
		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		if envelope.Type != protocol.MessageTypeCharacterReviewResult {
			continue
		}
		var msg protocol.CharacterReviewResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return protocol.CharacterReviewResultPayload{}, err
		}
		return msg.Payload, nil
	}
}

func TestServe_CharacterUpload_LevelOutOfRange_AutomaticallyRejectedWithoutCallingLLM(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1", Level: 12},
		},
	}
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
		Name: "review_character", Arguments: json.RawMessage(`{"verdict":"approve","reason":"fine"}`),
	}}}}
	policies := map[string]policy.CampaignPolicy{"campaign-review-level": {MinLevel: 1, MaxLevel: 5}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-review-level", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-review-level", "player-a", `{"name":"Kestrel"}`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}

	result, err := awaitReviewResult(ctx, conn)
	if err != nil {
		t.Fatalf("awaitReviewResult() error = %v", err)
	}
	if result.CharacterID != payload.CharacterID {
		t.Errorf("CharacterID = %q, want %q", result.CharacterID, payload.CharacterID)
	}
	if result.Status != string(store.CharacterStatusRejected) {
		t.Errorf("Status = %q, want %q", result.Status, store.CharacterStatusRejected)
	}
	if result.Reason == "" {
		t.Error("Reason is empty, want an explanation naming the level range")
	}
	if len(fakeLLM.calls) != 0 {
		t.Errorf("LLM was called %d time(s), want 0 — a deterministic level-range rejection needs no model opinion", len(fakeLLM.calls))
	}

	saved, err := st.GetCharacter(ctx, payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.Status != store.CharacterStatusRejected {
		t.Errorf("saved Status = %q, want %q", saved.Status, store.CharacterStatusRejected)
	}
}

func TestServe_CharacterUpload_LevelWithinRange_LLMApproves_SetsApprovedAndNotifies(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1", Level: 3},
		},
	}
	// respondFunc, not a single canned response: approving this character
	// also launches sendCharacterIntro (character_intro.go) in the
	// background against this same fake LLM, and its own system prompt
	// must not be handed the review's review_character tool call back —
	// it would spin through its own tool-call loop reacting to a tool it
	// doesn't recognize before giving up.
	fakeLLM := &fakeLLMProvider{respondFunc: func(_ int, req llm.CompletionRequest) (llm.CompletionResponse, error) {
		if isReviewCall(req) {
			return llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
				Name: "review_character", Arguments: json.RawMessage(`{"verdict":"approve","reason":"Looks reasonable for level 3."}`),
			}}}, nil
		}
		return llm.CompletionResponse{Text: "You find yourself somewhere quiet, for now."}, nil
	}}
	policies := map[string]policy.CampaignPolicy{"campaign-review-approve": {MinLevel: 1, MaxLevel: 5}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-review-approve", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-review-approve", "player-a", `{"name":"Kestrel"}`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}

	result, err := awaitReviewResult(ctx, conn)
	if err != nil {
		t.Fatalf("awaitReviewResult() error = %v", err)
	}
	if result.Status != string(store.CharacterStatusApproved) {
		t.Errorf("Status = %q, want %q", result.Status, store.CharacterStatusApproved)
	}
	if result.Reason != "Looks reasonable for level 3." {
		t.Errorf("Reason = %q, want the model's own stated reason", result.Reason)
	}
	// Counts only calls that carry the review's own system prompt —
	// character_intro.go's own background pass (see the fakeLLM comment
	// above) also calls this same fake LLM, asynchronously, so asserting
	// on the raw total would be racy against it; filtering by content
	// isn't.
	reviewCalls := 0
	fakeLLM.mu.Lock()
	for _, call := range fakeLLM.calls {
		if isReviewCall(call) {
			reviewCalls++
		}
	}
	fakeLLM.mu.Unlock()
	if reviewCalls != 1 {
		t.Fatalf("review LLM call happened %d time(s), want exactly 1", reviewCalls)
	}

	saved, err := st.GetCharacter(ctx, payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.Status != store.CharacterStatusApproved {
		t.Errorf("saved Status = %q, want %q", saved.Status, store.CharacterStatusApproved)
	}
}

func TestServe_CharacterUpload_LLMRejects_SetsRejectedAndNotifies(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
		Name: "review_character", Arguments: json.RawMessage(`{"verdict":"reject","reason":"Ability scores far exceed anything creation could produce."}`),
	}}}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-review-reject", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-review-reject", "player-a", `{"name":"Kestrel"}`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}

	result, err := awaitReviewResult(ctx, conn)
	if err != nil {
		t.Fatalf("awaitReviewResult() error = %v", err)
	}
	if result.Status != string(store.CharacterStatusRejected) {
		t.Errorf("Status = %q, want %q", result.Status, store.CharacterStatusRejected)
	}

	saved, err := st.GetCharacter(ctx, payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.Status != store.CharacterStatusRejected {
		t.Errorf("saved Status = %q, want %q", saved.Status, store.CharacterStatusRejected)
	}
}

func TestServe_CharacterUpload_NoLLMConfigured_StaysPendingReview(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-review-nollm", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-review-nollm", "player-a", `{"name":"Kestrel"}`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}

	// No LLM is configured and no level range either, so the review pass
	// concludes nothing — no character.review_result should ever arrive.
	// A short deadline read timing out is the real proof of that, not
	// just "we didn't happen to see one yet".
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortCancel()
	if _, err := awaitReviewResult(shortCtx, conn); err == nil {
		t.Fatal("received a character.review_result, want none — no LLM configured means no automatic decision")
	}

	saved, err := st.GetCharacter(context.Background(), payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.Status != store.CharacterStatusPendingReview {
		t.Errorf("saved Status = %q, want %q", saved.Status, store.CharacterStatusPendingReview)
	}
}

// isReviewCall reports whether req is runCharacterReviewPass's own
// completion call (character_review.go uses the SystemPrompt/UserPrompt
// fields, not Messages) — used to tell it apart from a concurrent
// sendCharacterIntro call against the same fake LLM (character_intro.go
// uses Messages instead), which a test approving a character will also
// trigger in the background.
func isReviewCall(req llm.CompletionRequest) bool {
	systemText := req.SystemPrompt
	if len(req.Messages) > 0 {
		systemText = req.Messages[0].Content
	}
	return strings.Contains(systemText, "reviewing a player-submitted character sheet")
}
