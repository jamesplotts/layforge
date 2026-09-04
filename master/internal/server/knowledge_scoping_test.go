// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// readDMProseMessages drains conn until it sees the turn's public,
// non-private narrative.dm_prose — every slow pass ends with exactly one
// broadcast to the whole campaign, so that message is a reliable
// terminator regardless of how many other broadcasts (player_bubble,
// tool.result) or private sends interleave before it on a given
// connection. Returns every dm_prose payload seen along the way,
// including the terminating public one.
func readDMProseMessages(ctx context.Context, t *testing.T, conn *websocket.Conn) []protocol.NarrativeDmProsePayload {
	t.Helper()
	var found []protocol.NarrativeDmProsePayload
	for i := 0; i < 20; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ != protocol.MessageTypeNarrativeDmProse {
			continue
		}
		var msg protocol.NarrativeDmProseMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshaling narrative.dm_prose: %v", err)
		}
		found = append(found, msg.Payload)
		if msg.Payload.Visibility == nil || msg.Payload.Visibility.Scope != protocol.VisibilityScopePrivate {
			return found
		}
	}
	t.Fatalf("did not see the turn's public dm_prose within 20 messages (seen so far: %+v)", found)
	return found
}

func TestServe_NarrativePlayerInput_SlowPass_NarratePrivately_Success_OnlyRecipientReceivesIt(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("narrate_privately", `{"character_ids":["char-b"],"text":"A private aside only Kestrel's ally notices."}`)
	policies := map[string]policy.CampaignPolicy{"campaign-private": {SharedKnowledge: policy.SharedKnowledgeStrict}}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-private", "player-a")
	seedCharacter(t, st, "char-b", "campaign-private", "player-b")

	connA := dialAndJoin(t, ts, "campaign-private", "player-a")
	defer connA.CloseNow()
	connB := dialAndJoin(t, ts, "campaign-private", "player-b")
	defer connB.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, connA, "campaign-private", "player-a", "char-a", "I do something only Kestrel would notice."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}

	// connB (the private recipient's own connection) must see it, with a
	// real private VisibilityScope attached.
	bProse := readDMProseMessages(ctx, t, connB)
	var sawPrivate bool
	for _, p := range bProse {
		if p.Text != "A private aside only Kestrel's ally notices." {
			continue
		}
		sawPrivate = true
		if p.Visibility == nil || p.Visibility.Scope != protocol.VisibilityScopePrivate {
			t.Errorf("private narration Visibility = %+v, want Scope=private", p.Visibility)
		}
		if len(p.Visibility.VisibleToCharacterIDs) != 1 || p.Visibility.VisibleToCharacterIDs[0] != "char-b" {
			t.Errorf("private narration VisibleToCharacterIDs = %v, want [char-b]", p.Visibility.VisibleToCharacterIDs)
		}
	}
	if !sawPrivate {
		t.Fatalf("connB never received the private narration (dm_prose seen: %+v)", bProse)
	}

	// connA (the acting player, not a recipient) must never see it, even
	// though connA triggered the turn.
	aProse := readDMProseMessages(ctx, t, connA)
	for _, p := range aProse {
		if p.Text == "A private aside only Kestrel's ally notices." {
			t.Errorf("connA received the private narration, want it excluded entirely (payload: %+v)", p)
		}
	}
}

func TestServe_NarrativePlayerInput_SlowPass_NarratePrivately_SharedKnowledgeNotStrict_ToolNotOffered(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	// party_omniscient (the default) — narrate_privately shouldn't be
	// offered at all, so this fake just returns plain narration for
	// every call.
	fakeLLM := &fakeLLMProvider{response: llm.CompletionResponse{Text: "The scene continues in the open, for everyone."}}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-not-strict", "player-a")

	conn := dialAndJoin(t, ts, "campaign-not-strict", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-not-strict", "player-a", "char-a", "I act."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	// Wait for the slow pass to actually finish before asserting on
	// fakeLLM's recorded calls, same reasoning as the imagegen
	// ToolIsOffered/ToolNotOffered tests.
	var prose protocol.NarrativeDmProseMessage
	if err := wsjson.Read(ctx, conn, &prose); err != nil {
		t.Fatalf("Read(narrative.dm_prose) error = %v", err)
	}

	slowPassCall := fakeLLM.callAt(t, 1)
	for _, tool := range slowPassCall.Tools {
		if tool.Name == "narrate_privately" {
			t.Errorf("slow pass Tools includes narrate_privately, want it absent when shared_knowledge isn't strict")
		}
	}
}

func TestServe_NarrativePlayerInput_SlowPass_NarratePrivately_NPCRecipient_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("narrate_privately", `{"character_ids":["npc-a"],"text":"A private aside."}`)
	policies := map[string]policy.CampaignPolicy{"campaign-private-npc": {SharedKnowledge: policy.SharedKnowledgeStrict}}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()
	// No owner — an NPC.
	seedCharacter(t, st, "npc-a", "campaign-private-npc", "")

	conn := dialAndJoin(t, ts, "campaign-private-npc", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-private-npc", "player-a", "npc-a", "The DM tries to narrate privately to an NPC."); err != nil {
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
	if toolResult.Payload.ReasonCode != "not_a_player_character" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "not_a_player_character")
	}
}

func TestSendHistory_PrivateEvent_HiddenFromNonRecipient_VisibleToRecipient(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("narrate_privately", `{"character_ids":["char-b"],"text":"Only Kestrel's ally should ever see this in history."}`)
	policies := map[string]policy.CampaignPolicy{"campaign-history-private": {SharedKnowledge: policy.SharedKnowledgeStrict}}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine, policy.NewJSONFileProvider(policies))
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-history-private", "player-a")
	seedCharacter(t, st, "char-b", "campaign-history-private", "player-b")

	connA := dialAndJoin(t, ts, "campaign-history-private", "player-a")
	defer connA.CloseNow()
	connB := dialAndJoin(t, ts, "campaign-history-private", "player-b")
	defer connB.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, connA, "campaign-history-private", "player-a", "char-a", "I do something only Kestrel would notice."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	// Drain both connections through the turn's public dm_prose so the
	// private send/record has actually completed before requesting
	// history.
	readDMProseMessages(ctx, t, connA)
	readDMProseMessages(ctx, t, connB)

	historyReq := protocol.HistoryRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "history-a",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-history-private",
			Type:            protocol.MessageTypeLogHistoryRequest,
		},
	}
	if err := wsjson.Write(ctx, connA, historyReq); err != nil {
		t.Fatalf("Write(log.history_request) from player-a error = %v", err)
	}
	var historyRespA protocol.HistoryResponseMessage
	if err := wsjson.Read(ctx, connA, &historyRespA); err != nil {
		t.Fatalf("Read(log.history_response) for player-a error = %v", err)
	}
	for _, raw := range historyRespA.Payload.Events {
		if containsText(t, raw, "Only Kestrel's ally should ever see this in history.") {
			t.Error("player-a's history includes the private narration, want it filtered out")
		}
	}

	historyReq.Envelope.MessageID = "history-b"
	historyReq.Envelope.SenderID = "player-b"
	if err := wsjson.Write(ctx, connB, historyReq); err != nil {
		t.Fatalf("Write(log.history_request) from player-b error = %v", err)
	}
	var historyRespB protocol.HistoryResponseMessage
	if err := wsjson.Read(ctx, connB, &historyRespB); err != nil {
		t.Fatalf("Read(log.history_response) for player-b error = %v", err)
	}
	found := false
	for _, raw := range historyRespB.Payload.Events {
		if containsText(t, raw, "Only Kestrel's ally should ever see this in history.") {
			found = true
		}
	}
	if !found {
		t.Error("player-b's history is missing the private narration, want it included (they're the real recipient)")
	}
}

func containsText(t *testing.T, raw json.RawMessage, text string) bool {
	t.Helper()
	var envelope struct {
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	return envelope.Payload.Text == text
}
