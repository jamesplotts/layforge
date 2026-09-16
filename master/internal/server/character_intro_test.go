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
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// awaitClientDisplay reads messages from conn until a client.display
// arrives (or the context expires) — sendCharacterIntro runs in its own
// background goroutine (character_intro.go), so it's a later, separate
// message after character.validation_result, not something a single
// conn.Read can assume is next.
func awaitClientDisplay(ctx context.Context, conn *websocket.Conn) (protocol.ClientDisplayPayload, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return protocol.ClientDisplayPayload{}, err
		}
		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		if envelope.Type != protocol.MessageTypeClientDisplay {
			continue
		}
		var msg protocol.ClientDisplayMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return protocol.ClientDisplayPayload{}, err
		}
		return msg.Payload, nil
	}
}

// awaitNarrativeDmThinking is awaitClientDisplay's narrative.dm_thinking
// counterpart — sendDmThinkingIndicatorAfterDelay (dm_slow_pass.go) sends
// it, if at all, only after dmThinkingIndicatorDelay has passed with the
// pass still running, so it's likewise not something a single conn.Read
// can assume is the very next message.
func awaitNarrativeDmThinking(ctx context.Context, conn *websocket.Conn) (protocol.NarrativeDmThinkingPayload, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return protocol.NarrativeDmThinkingPayload{}, err
		}
		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		if envelope.Type != protocol.MessageTypeNarrativeDmThinking {
			continue
		}
		var msg protocol.NarrativeDmThinkingMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return protocol.NarrativeDmThinkingPayload{}, err
		}
		return msg.Payload, nil
	}
}

// TestServe_CharacterIntro_SlowLLM_SendsPrivateDmThinkingIndicatorFirst
// is the regression test for a live-reported UX gap: "right after
// rolling a character, there is a very long delay before the first DM
// message appears" — sendCreationComplete now launches a delayed,
// private narrative.dm_thinking indicator (dmThinkingText) alongside
// sendCharacterIntro, the same "done channel cancels a too-early send"
// shape renderPlayerBubble's own slow-pass indicator uses. The fake
// LLM's first call (the intro pass's own — nothing else touches s.llm
// during a quick_roll) deliberately sleeps well past
// dmThinkingIndicatorDelay so this test can observe the indicator
// actually firing before the intro itself arrives, without adding any
// real delay to TestServe_CharacterIntro_QuickRoll_SendsPrivateGroundedNarration
// above.
func TestServe_CharacterIntro_SlowLLM_SendsPrivateDmThinkingIndicatorFirst(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{
		"name": "Bram", "gender": "Male", "raceName": "Dwarf", "background": "Criminal",
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		startCharacterCreationResp: &systemenginepb.CharacterCreationPromptResponse{
			Success: true, Done: true,
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	const introText = "You've spent years running with a gang in the sewers beneath the city."
	fakeLLM := &fakeLLMProvider{respondFunc: func(callIndex int, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		if callIndex == 0 {
			time.Sleep(2 * time.Second)
		}
		return llm.CompletionResponse{Text: introText}, nil
	}}

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-intro-thinking", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sendCreationStartNamed(ctx, conn, "campaign-intro-thinking", "player-a", "Bram"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-intro-thinking", "player-a", topPrompt, "quick_roll")

	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}

	thinking, err := awaitNarrativeDmThinking(ctx, conn)
	if err != nil {
		t.Fatalf("awaitNarrativeDmThinking() error = %v", err)
	}
	if thinking.Text == "" {
		t.Error("Payload.Text is empty, want a non-empty display string")
	}
	if thinking.Recipient != "player-a" {
		t.Errorf("Payload.Recipient = %q, want %q — private to this player, not a broadcast", thinking.Recipient, "player-a")
	}

	intro, err := awaitClientDisplay(ctx, conn)
	if err != nil {
		t.Fatalf("awaitClientDisplay() error = %v", err)
	}
	if intro.Text != introText {
		t.Errorf("intro Text = %q, want %q", intro.Text, introText)
	}
}

func TestServe_CharacterIntro_QuickRoll_SendsPrivateGroundedNarration(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{
		"name": "Bram", "gender": "Male", "raceName": "Dwarf", "background": "Criminal",
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		startCharacterCreationResp: &systemenginepb.CharacterCreationPromptResponse{
			Success: true, Done: true,
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	const introText = "You've spent years running with a gang in the sewers beneath the city."
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: introText}}

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-intro", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sendCreationStartNamed(ctx, conn, "campaign-intro", "player-a", "Bram"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-intro", "player-a", topPrompt, "quick_roll")

	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}
	if validation.Payload.CharacterID == "" {
		t.Fatal("Payload.CharacterID is empty, want a generated id")
	}

	intro, err := awaitClientDisplay(ctx, conn)
	if err != nil {
		t.Fatalf("awaitClientDisplay() error = %v", err)
	}
	if intro.Text != introText {
		t.Errorf("intro Text = %q, want %q", intro.Text, introText)
	}
	if intro.Visibility == nil || intro.Visibility.Scope != protocol.VisibilityScopePrivate {
		t.Fatalf("intro Visibility = %+v, want a private scope", intro.Visibility)
	}
	if len(intro.Visibility.VisibleToCharacterIDs) != 1 || intro.Visibility.VisibleToCharacterIDs[0] != validation.Payload.CharacterID {
		t.Errorf("VisibleToCharacterIDs = %v, want exactly [%q]", intro.Visibility.VisibleToCharacterIDs, validation.Payload.CharacterID)
	}

	// The character's own real data reached the pass — the fake records
	// every call it received, so this confirms grounding, not just that
	// some text arrived.
	if len(fakeLLM.calls) == 0 {
		t.Fatal("LLM was never called for the character intro")
	}
	sawCharacterData := false
	for _, call := range fakeLLM.calls {
		for _, msg := range call.Messages {
			if strings.Contains(msg.Content, "Criminal") && strings.Contains(msg.Content, "Bram") {
				sawCharacterData = true
			}
		}
	}
	if !sawCharacterData {
		t.Error("no LLM call received the character's own data (name/background)")
	}
}

func TestServe_CharacterIntro_NoLLMConfigured_NoIntroSent(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Ari"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		startCharacterCreationResp: &systemenginepb.CharacterCreationPromptResponse{
			Success: true, Done: true,
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-2", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	ts, _ := newTestServerForCreation(t, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-intro-nollm", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-intro-nollm", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-intro-nollm", "player-a", topPrompt, "quick_roll")

	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}

	// newTestServerForCreation wires no LLM at all — sendCharacterIntro's
	// own s.llm == nil guard should mean no client.display ever follows.
	// A short deadline read timing out is the real proof of that.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortCancel()
	if _, err := awaitClientDisplay(shortCtx, conn); err == nil {
		t.Fatal("received a client.display, want none — no LLM configured means no character intro")
	}
}
