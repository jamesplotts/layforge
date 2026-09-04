// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// spotlightTurnCounter guarantees a unique message_id per runTurnAndWait
// call — sendPlayerInput's own MessageID ("input-"+sender) collides
// across repeated turns from the same sender, and a repeated message_id
// is a real, deliberate no-op per AppendEvent's dedup contract (design
// doc §5), which would silently undercount recorded turns for these
// tests specifically (they drive several turns from the same sender on
// purpose, unlike every other test in this package).
var spotlightTurnCounter int64

// runTurnAndWait sends one narrative.player_input for characterID on
// conn and drains its two expected messages (narrative.player_bubble
// then narrative.dm_prose) before returning — callers need this so a
// following turn's fast-pass Complete() call can't interleave with this
// turn's still-in-flight slow pass, the same reasoning documented on
// every other test in this package that drives more than one turn.
func runTurnAndWait(ctx context.Context, t *testing.T, conn *websocket.Conn, campaignID, sender, characterID, text string) {
	t.Helper()
	n := atomic.AddInt64(&spotlightTurnCounter, 1)
	input := protocol.NarrativePlayerInputMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       fmt.Sprintf("spotlight-input-%d", n),
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeNarrativePlayerInput,
		},
		Payload: protocol.NarrativePlayerInputPayload{CharacterID: characterID, Text: text, Source: protocol.NarrativeInputSourceTyped},
	}
	if err := wsjson.Write(ctx, conn, input); err != nil {
		t.Fatalf("Write(narrative.player_input) error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}
}

// userMessageContent returns the content of the last RoleUser message in
// req.Messages — the slow pass builds its context via Messages, not
// SystemPrompt/UserPrompt (those are the fast pass's single-turn-only
// fields; see llm.CompletionRequest's own doc comment), so this is where
// spotlightContextText's output actually lands for a slow-pass call.
func userMessageContent(t *testing.T, req llm.CompletionRequest) string {
	t.Helper()
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleUser {
			return req.Messages[i].Content
		}
	}
	t.Fatalf("CompletionRequest has no RoleUser message: %+v", req)
	return ""
}

func TestServe_NarrativePlayerInput_SlowPass_SpotlightBalance_NeverSpokenCharacter_FlaggedNoTurns(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-spotlight-never", "player-a")
	seedCharacter(t, st, "char-b", "campaign-spotlight-never", "player-b")

	conn := dialAndJoin(t, ts, "campaign-spotlight-never", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		runTurnAndWait(ctx, t, conn, "campaign-spotlight-never", "player-a", "char-a", "I press onward.")
	}

	content := userMessageContent(t, fakeLLM.callAt(t, 5)) // 3 turns * 2 calls (fast, slow) - 1
	if !strings.Contains(content, "Spotlight balance") {
		t.Fatalf("slow pass user content = %q, want it to include a Spotlight balance section", content)
	}
	if !strings.Contains(content, "- char-b: no turns in recent history") {
		t.Errorf("slow pass user content = %q, want a line flagging char-b as having no turns in recent history", content)
	}
	if strings.Contains(content, "- char-a:") {
		t.Errorf("slow pass user content = %q, want char-a (who just acted) not flagged as quiet", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_SpotlightBalance_PreviouslyActiveCharacter_ReportsCorrectCount(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-spotlight-count", "player-a")
	seedCharacter(t, st, "char-b", "campaign-spotlight-count", "player-b")

	// A single connection drives every turn here — narrative.player_input
	// isn't ownership-checked against the sending connection, and
	// narrative.dm_prose/player_bubble broadcast to the whole campaign
	// regardless, so a second real connection would just mean draining
	// its own copies of every broadcast too, for no assertion this test
	// actually needs.
	conn := dialAndJoin(t, ts, "campaign-spotlight-count", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// turns, in order: a, b, a, a, a — char-b's last turn is 3 turns back
	// from the one about to be sent.
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-count", "player-a", "char-a", "I act.")
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-count", "player-b", "char-b", "I act.")
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-count", "player-a", "char-a", "I act.")
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-count", "player-a", "char-a", "I act.")
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-count", "player-a", "char-a", "I act.")

	content := userMessageContent(t, fakeLLM.callAt(t, 9)) // 5 turns * 2 calls - 1
	if !strings.Contains(content, "- char-b: 3 turn(s) since their last turn") {
		t.Errorf("slow pass user content = %q, want char-b flagged with 3 turns since their last turn", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_SpotlightBalance_OnlyOnePlayerCharacter_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-spotlight-solo", "player-a")

	conn := dialAndJoin(t, ts, "campaign-spotlight-solo", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-spotlight-solo", "player-a", "char-a", "I act alone.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Spotlight balance") {
		t.Errorf("slow pass user content = %q, want no Spotlight balance section with only one player character", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_SpotlightBalance_AlternatingTurns_FlagsTheOtherPlayerAtOne(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-spotlight-balanced", "player-a")
	seedCharacter(t, st, "char-b", "campaign-spotlight-balanced", "player-b")

	conn := dialAndJoin(t, ts, "campaign-spotlight-balanced", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-spotlight-balanced", "player-a", "char-a", "I act.")
	runTurnAndWait(ctx, t, conn, "campaign-spotlight-balanced", "player-b", "char-b", "I act.")

	// With only two players strictly alternating, "1 turn since their
	// last turn" is real information the soft signal is right to
	// surface every time — it's only ever silent when the same
	// character goes twice in a row.
	content := userMessageContent(t, fakeLLM.callAt(t, 3)) // 2 turns * 2 calls - 1, char-b's own turn
	if !strings.Contains(content, "- char-a: 1 turn(s) since their last turn") {
		t.Errorf("slow pass user content = %q, want char-a flagged with 1 turn since their last turn", content)
	}
	if strings.Contains(content, "- char-b:") {
		t.Errorf("slow pass user content = %q, want char-b (who just acted) not flagged as quiet", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_SpotlightBalance_NPCExcluded(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-spotlight-npc", "player-a")
	// An NPC create_npc would have saved under masterSenderID ("master")
	// — must never be reported as a "quiet player".
	seedCharacter(t, st, "npc-1", "campaign-spotlight-npc", "master")

	conn := dialAndJoin(t, ts, "campaign-spotlight-npc", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-spotlight-npc", "player-a", "char-a", "I act.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Spotlight balance") {
		t.Errorf("slow pass user content = %q, want no Spotlight balance section — the only other character is an NPC, not a player", content)
	}
}
