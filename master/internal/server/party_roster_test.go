// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
)

func TestServe_NarrativePlayerInput_SlowPass_PartyRoster_OtherPlayers_ListedWithExactIDs(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-roster", "player-a")
	seedCharacter(t, st, "char-b", "campaign-roster", "player-b")
	seedCharacter(t, st, "char-c", "campaign-roster", "player-c")

	conn := dialAndJoin(t, ts, "campaign-roster", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-roster", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if !strings.Contains(content, "Other real player characters in this campaign") {
		t.Fatalf("slow pass user content = %q, want a party roster section", content)
	}
	if !strings.Contains(content, `- char-b: {"name":"Kestrel"}`) {
		t.Errorf("slow pass user content = %q, want char-b listed with its own character data", content)
	}
	if !strings.Contains(content, `- char-c: {"name":"Kestrel"}`) {
		t.Errorf("slow pass user content = %q, want char-c listed with its own character data", content)
	}
	if strings.Contains(content, "- char-a:") {
		t.Errorf("slow pass user content = %q, want the acting character (char-a) excluded from its own roster", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_PartyRoster_SoloPlayer_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-roster-solo", "player-a")

	conn := dialAndJoin(t, ts, "campaign-roster-solo", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-roster-solo", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Other real player characters") {
		t.Errorf("slow pass user content = %q, want no party roster section for a solo player", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_PartyRoster_NPCExcluded(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, nil)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-roster-npc", "player-a")
	// An NPC create_npc would have saved under masterSenderID ("master")
	// — must never be offered as a valid tool-call target for another
	// player's own action.
	seedCharacter(t, st, "npc-1", "campaign-roster-npc", "master")

	conn := dialAndJoin(t, ts, "campaign-roster-npc", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-roster-npc", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Other real player characters") {
		t.Errorf("slow pass user content = %q, want no party roster section — the only other character is an NPC", content)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_PartyRoster_NoCharactersStore_NoSectionAtAll(t *testing.T) {
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues."}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, fakeLLM, "test-model", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, session.NewHub()).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-roster-nostore", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runTurnAndWait(ctx, t, conn, "campaign-roster-nostore", "player-a", "char-a", "I look around.")

	content := userMessageContent(t, fakeLLM.callAt(t, 1))
	if strings.Contains(content, "Other real player characters") {
		t.Errorf("slow pass user content = %q, want no party roster section with no characters store configured", content)
	}
}
