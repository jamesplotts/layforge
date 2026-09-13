// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// TestServe_NarrativePlayerInput_SlowPass_RecentConversation_CarriesPriorTurnForward
// is the direct regression test for the live-observed continuity bug:
// without recentConversationContextText, a second turn's slow pass had
// no idea what happened during the first turn at all.
func TestServe_NarrativePlayerInput_SlowPass_RecentConversation_CarriesPriorTurnForward(t *testing.T) {
	// respondFunc, not a fixed responses list: each turn is actually
	// three LLM calls (fast pass, then the slow pass's own mechanics and
	// narration sub-passes — see dm_slow_pass.go), not one, so branching
	// on which system prompt a call carries is more robust than guessing
	// exact call indices.
	fastTexts := []string{"Kestrel draws a sword.", "Kestrel sheathes the blade again."}
	narrationTexts := []string{"The blade catches the torchlight.", "The tension in the room eases."}
	fastCalls, narrationCalls := 0, 0
	fakeLLM := &fakeLLMProvider{respondFunc: func(_ int, req llm.CompletionRequest) (llm.CompletionResponse, error) {
		switch {
		case strings.Contains(req.SystemPrompt, "rendering a tabletop RPG player's stated action"):
			text := fastTexts[fastCalls]
			fastCalls++
			return llm.CompletionResponse{Text: text}, nil
		case len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "writing the narration for what a player's character just did"):
			text := narrationTexts[narrationCalls]
			narrationCalls++
			return llm.CompletionResponse{Text: text}, nil
		default:
			// The mechanics sub-pass: no mechanical action needed, so it
			// settles immediately with no tool calls (its own text is
			// discarded regardless — see runMechanicsPass).
			return llm.CompletionResponse{Text: ""}, nil
		}
	}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-recent", "player-a")

	conn := dialAndJoin(t, ts, "campaign-recent", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-recent", "player-a", "char-1", "I draw my sword."); err != nil {
		t.Fatalf("sendPlayerInput() #1 error = %v", err)
	}
	var bubble1 protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble1); err != nil {
		t.Fatalf("Read(narrative.player_bubble) #1 error = %v", err)
	}
	var prose1 protocol.ClientDisplayMessage
	if err := wsjson.Read(ctx, conn, &prose1); err != nil {
		t.Fatalf("Read(client.display) #1 error = %v", err)
	}

	if _, err := sendPlayerInput(ctx, conn, "campaign-recent", "player-a", "char-1", "I sheathe my sword."); err != nil {
		t.Fatalf("sendPlayerInput() #2 error = %v", err)
	}
	var bubble2 protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble2); err != nil {
		t.Fatalf("Read(narrative.player_bubble) #2 error = %v", err)
	}
	var prose2 protocol.ClientDisplayMessage
	if err := wsjson.Read(ctx, conn, &prose2); err != nil {
		t.Fatalf("Read(client.display) #2 error = %v", err)
	}

	// Find turn 2's own narration-pass call by content rather than a
	// fixed index — how many mechanics-pass iterations happen isn't this
	// test's concern. Its grounding context (Messages[1]) is identical to
	// its sibling mechanics-pass call for the same turn (both pulled from
	// the one shared slowPassGroundingContext), so any call carrying both
	// the narration system prompt and this turn's own action uniquely
	// identifies it.
	fakeLLM.mu.Lock()
	var userContent string
	for _, call := range fakeLLM.calls {
		if len(call.Messages) < 2 {
			continue
		}
		if strings.Contains(call.Messages[0].Content, "writing the narration for what a player's character just did") &&
			strings.Contains(call.Messages[1].Content, "I sheathe my sword") {
			userContent = call.Messages[1].Content
			break
		}
	}
	fakeLLM.mu.Unlock()
	if userContent == "" {
		t.Fatal("no narration-pass call found for turn 2")
	}

	if !strings.Contains(userContent, "Kestrel draws a sword.") {
		t.Errorf("turn 2's context is missing turn 1's player bubble; got %q", userContent)
	}
	if !strings.Contains(userContent, "The blade catches the torchlight.") {
		t.Errorf("turn 2's context is missing turn 1's DM narration; got %q", userContent)
	}
	firstTurnIndex := strings.Index(userContent, "Kestrel draws a sword.")
	secondActionIndex := strings.Index(userContent, "Player action: I sheathe my sword.")
	if firstTurnIndex == -1 || secondActionIndex == -1 || firstTurnIndex > secondActionIndex {
		t.Errorf("turn 1's exchange must appear before turn 2's own player action; got %q", userContent)
	}
}

// TestServe_NarrativePlayerInput_SlowPass_RecentConversation_ExcludesPrivateDisplay
// covers design doc §9.7: a private client.display (narrate_privately,
// or a character's own one-time creation intro) that already happened
// must never resurface in a later, unrelated public turn's own
// "memory" — it belongs to whichever character it was scoped to, not to
// the shared conversation thread every future turn gets fed.
func TestServe_NarrativePlayerInput_SlowPass_RecentConversation_ExcludesPrivateDisplay(t *testing.T) {
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Kestrel looks around."}, // fast pass
			{Text: "Nothing seems amiss."},  // slow pass
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-recent-private", "player-a")

	const secretText = "A voice only Kestrel can hear whispers of betrayal."
	privatePayload, err := json.Marshal(protocol.ClientDisplayMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "private-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "master",
			CampaignID:      "campaign-recent-private",
			Type:            protocol.MessageTypeClientDisplay,
		},
		Payload: protocol.ClientDisplayPayload{
			Text: secretText,
			Visibility: &protocol.VisibilityScope{
				Scope:                 protocol.VisibilityScopePrivate,
				VisibleToCharacterIDs: []string{"char-1"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := st.AppendEvent(context.Background(), store.Event{
		CampaignID:  "campaign-recent-private",
		MessageID:   "private-1",
		MessageType: string(protocol.MessageTypeClientDisplay),
		SenderID:    "master",
		OccurredAt:  time.Now().UTC(),
		Raw:         privatePayload,
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-recent-private", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-recent-private", "player-a", "char-1", "I look around."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var prose protocol.ClientDisplayMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(client.display) error = %v", err)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	userContent := slowPassCall.Messages[1].Content
	if strings.Contains(userContent, secretText) {
		t.Errorf("recent-conversation context leaked a private client.display: %q", userContent)
	}
}
