// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestHandleWebSocket_ValidConnect_RespondsWithSessionStateJoined(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "test-client",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{
			ClientKind: "player_web_v1",
			AuthToken:  "test-token",
		},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var got protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}

	if got.Type != protocol.MessageTypeSystemSessionState {
		t.Errorf("Type = %q, want %q", got.Type, protocol.MessageTypeSystemSessionState)
	}
	if got.Payload.State != protocol.SessionStateJoined {
		t.Errorf("Payload.State = %q, want %q", got.Payload.State, protocol.SessionStateJoined)
	}
	if got.CampaignID != connect.CampaignID {
		t.Errorf("CampaignID = %q, want %q", got.CampaignID, connect.CampaignID)
	}
	if err := got.Envelope.Validate(); err != nil {
		t.Errorf("response Envelope.Validate() = %v, want nil", err)
	}
}

func TestHandleWebSocket_MissingCampaignID_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-2",
			Timestamp:       time.Now().UTC(),
			SenderID:        "test-client",
			// CampaignID intentionally omitted.
			Type: protocol.MessageTypeSystemConnect,
		},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}

	if got.Type != protocol.MessageTypeSystemError {
		t.Errorf("Type = %q, want %q", got.Type, protocol.MessageTypeSystemError)
	}
	if got.Payload.InReplyToMessageID != connect.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", got.Payload.InReplyToMessageID, connect.MessageID)
	}
	if got.Payload.Code == "" {
		t.Error("Payload.Code is empty, want a reason code")
	}
}

func TestHandleWebSocket_WrongFirstMessageType_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	// A well-formed envelope, but not the required first message type.
	notConnect := protocol.SystemErrorMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-3",
			Timestamp:       time.Now().UTC(),
			SenderID:        "test-client",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSystemError,
		},
		Payload: protocol.SystemErrorPayload{Code: "irrelevant", Message: "irrelevant"},
	}
	if err := wsjson.Write(ctx, conn, notConnect); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.InReplyToMessageID != notConnect.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", got.Payload.InReplyToMessageID, notConnect.MessageID)
	}
}

func TestHandleWebSocket_ValidConnect_PersistsHandshakeEvents(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	events, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })

	srv := server.New(logger, events, nil, "", nil, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-persist",
			Timestamp:       time.Now().UTC(),
			SenderID:        "test-client",
			CampaignID:      "campaign-persist",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "player_web_v1", AuthToken: "test-token"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var got protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}

	// The read above only proves the response was sent; recordEvent runs
	// synchronously before that write, so by the time we get here both
	// the inbound connect and the outbound session_state are already
	// durable — no need to poll or sleep.
	recorded, _, err := events.ListEvents(ctx, "campaign-persist", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("len(recorded) = %d, want 2 (connect + session_state); got %+v", len(recorded), recorded)
	}
	if recorded[0].MessageType != string(protocol.MessageTypeSystemConnect) {
		t.Errorf("recorded[0].MessageType = %q, want %q", recorded[0].MessageType, protocol.MessageTypeSystemConnect)
	}
	if recorded[0].MessageID != connect.MessageID {
		t.Errorf("recorded[0].MessageID = %q, want %q", recorded[0].MessageID, connect.MessageID)
	}
	if recorded[1].MessageType != string(protocol.MessageTypeSystemSessionState) {
		t.Errorf("recorded[1].MessageType = %q, want %q", recorded[1].MessageType, protocol.MessageTypeSystemSessionState)
	}
}

func TestServe_SafetyFlag_BroadcastsToAllClientsInCampaign(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	a := dialAndJoin(t, ts, "campaign-broadcast", "player-a")
	defer a.CloseNow()
	b := dialAndJoin(t, ts, "campaign-broadcast", "player-b")
	defer b.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-broadcast",
			Type:            protocol.MessageTypeSafetyFlag,
		},
		Payload: protocol.SafetyFlagPayload{Topic: "combat"},
	}
	if err := wsjson.Write(ctx, a, flag); err != nil {
		t.Fatalf("Write(safety.flag) error = %v", err)
	}

	// Both the flagging client and the other client at the table must
	// receive the broadcast (design doc §9.2: the scene interrupts for
	// everyone, sender included) — and neither copy may attribute it to
	// player-a, since the whole point is not naming who flagged.
	for name, conn := range map[string]*websocket.Conn{"player-a (sender)": a, "player-b": b} {
		var got protocol.SafetyFlagBroadcastMessage
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("%s: Read(safety.flag_broadcast) error = %v", name, err)
		}
		if got.Type != protocol.MessageTypeSafetyFlagBroadcast {
			t.Errorf("%s: Type = %q, want %q", name, got.Type, protocol.MessageTypeSafetyFlagBroadcast)
		}
		if got.Payload.Topic != "combat" {
			t.Errorf("%s: Payload.Topic = %q, want %q", name, got.Payload.Topic, "combat")
		}
		if got.SenderID != "master" {
			t.Errorf("%s: SenderID = %q, want %q (must not attribute the broadcast to whoever flagged)", name, got.SenderID, "master")
		}
	}
}

func TestServe_SafetyFlag_DoesNotCrossCampaigns(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	inCampaign1 := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer inCampaign1.CloseNow()
	inCampaign2 := dialAndJoin(t, ts, "campaign-2", "player-b")
	defer inCampaign2.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, inCampaign1, flag); err != nil {
		t.Fatalf("Write(safety.flag) error = %v", err)
	}

	var got protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, inCampaign1, &got); err != nil {
		t.Fatalf("campaign-1 client: Read(safety.flag_broadcast) error = %v", err)
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()
	if _, _, err := inCampaign2.Read(shortCtx); err == nil {
		t.Error("campaign-2 client: received a message, want nothing (campaigns must not cross)")
	}
}

func TestServe_MalformedJSON_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Bypass wsjson.Write, which can only ever produce valid JSON, to
	// exercise dispatch's malformed-message path — realistic behavior
	// from a buggy third-party client (design doc §4: third-party
	// clients are first-class protocol consumers, not guaranteed
	// well-behaved ones).
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{not valid json`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var gotErr protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &gotErr); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if gotErr.Type != protocol.MessageTypeSystemError {
		t.Errorf("Type = %q, want %q", gotErr.Type, protocol.MessageTypeSystemError)
	}
	if gotErr.Payload.InReplyToMessageID != "" {
		t.Errorf("Payload.InReplyToMessageID = %q, want empty (unparseable message has no message_id to point at)", gotErr.Payload.InReplyToMessageID)
	}

	// Connection must still be usable afterward, same as any other
	// rejected message.
	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-after-malformed",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) after malformed message error = %v", err)
	}
	var gotBroadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &gotBroadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) after malformed message error = %v", err)
	}
}

func TestServe_UnsupportedMessageType_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// system.connect is a recognized, valid message type, but not one
	// serve's dispatch handles after the handshake — a good stand-in for
	// "any message type not yet implemented" without needing a made-up
	// invalid type.
	again := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "unsupported-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSystemConnect,
		},
	}
	if err := wsjson.Write(ctx, conn, again); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var gotErr protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &gotErr); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if gotErr.Payload.InReplyToMessageID != again.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", gotErr.Payload.InReplyToMessageID, again.MessageID)
	}

	// The connection must still be usable afterward — one unsupported
	// message shouldn't end the session.
	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-after-error",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) after error error = %v", err)
	}
	var gotBroadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &gotBroadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) after error error = %v", err)
	}
	if gotBroadcast.Type != protocol.MessageTypeSafetyFlagBroadcast {
		t.Errorf("Type = %q, want %q", gotBroadcast.Type, protocol.MessageTypeSafetyFlagBroadcast)
	}
}

// newTestServerWithHistory is newTestServer, but backed by a real
// in-memory SQLite store instead of nil — for tests that exercise
// log.history_request, which needs actual persistence to answer from.
func newTestServerWithHistory(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	events, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })
	return httptest.NewServer(server.New(logger, events, nil, "", nil, nil, nil).Handler())
}

func TestServe_LogHistoryRequest_ReturnsRecordedEventsInOrder(t *testing.T) {
	ts := newTestServerWithHistory(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-history", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-history",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) error = %v", err)
	}
	var broadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &broadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) error = %v", err)
	}

	// By now three events are on record for this campaign: this
	// connection's own system.connect, the system.session_state Master
	// replied with, and the safety.flag just sent (its broadcast landed
	// as a fourth, but arrived after this request would be built, so
	// isn't asserted on below to keep the test independent of exactly
	// when Master finishes persisting it relative to this request).
	histReq := protocol.HistoryRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "hist-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-history",
			Type:            protocol.MessageTypeLogHistoryRequest,
		},
	}
	if err := wsjson.Write(ctx, conn, histReq); err != nil {
		t.Fatalf("Write(log.history_request) error = %v", err)
	}

	var got protocol.HistoryResponseMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(log.history_response) error = %v", err)
	}
	if got.Type != protocol.MessageTypeLogHistoryResponse {
		t.Errorf("Type = %q, want %q", got.Type, protocol.MessageTypeLogHistoryResponse)
	}
	if len(got.Payload.Events) < 3 {
		t.Fatalf("len(Events) = %d, want at least 3 (connect, session_state, safety.flag)", len(got.Payload.Events))
	}

	wantTypes := []string{"system.connect", "system.session_state", "safety.flag"}
	for i, wantType := range wantTypes {
		var envelope protocol.Envelope
		if err := json.Unmarshal(got.Payload.Events[i], &envelope); err != nil {
			t.Fatalf("Events[%d]: Unmarshal() error = %v", i, err)
		}
		if string(envelope.Type) != wantType {
			t.Errorf("Events[%d].type = %q, want %q", i, envelope.Type, wantType)
		}
	}
}

func TestServe_LogHistoryRequest_Default_ReturnsMostRecentEvents(t *testing.T) {
	ts := newTestServerWithHistory(t)
	defer ts.Close()

	// Generate enough traffic that a small Limit can't happen to capture
	// everything by coincidence — the point is proving the default page
	// picks out the tail specifically, not just "some events".
	conn := dialAndJoin(t, ts, "campaign-tail", "player-a")
	defer conn.CloseNow()
	for i := 0; i < 3; i++ {
		sendSafetyFlagAndAwaitBroadcast(t, conn, "campaign-tail", "player-a", fmt.Sprintf("flag-%d", i))
	}

	// By now: connect, session_state, then 3x (flag, broadcast) = 8
	// events on record. Ask for just the most recent 2 with no anchor.
	page, err := requestHistory(context.Background(), conn, "campaign-tail", "player-a", protocol.HistoryRequestPayload{Limit: 2})
	if err != nil {
		t.Fatalf("requestHistory() error = %v", err)
	}
	if !page.HasMore {
		t.Error("HasMore = false, want true (6 older events exist)")
	}
	if len(page.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(page.Events))
	}
	wantTypes := []string{"safety.flag", "safety.flag_broadcast"}
	for i, wantType := range wantTypes {
		var envelope protocol.Envelope
		if err := json.Unmarshal(page.Events[i], &envelope); err != nil {
			t.Fatalf("Events[%d]: Unmarshal() error = %v", i, err)
		}
		if string(envelope.Type) != wantType {
			t.Errorf("Events[%d].type = %q, want %q (the last thing sent, not the campaign's first message)", i, envelope.Type, wantType)
		}
	}
}

func TestServe_LogHistoryRequest_BeforeSequence_WalksBackward(t *testing.T) {
	ts := newTestServerWithHistory(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-page", "player-a")
	defer conn.CloseNow()
	ctx := context.Background()

	// Two events on record from the handshake (connect, session_state).
	// Page 1 (default/tail, limit=1) should be session_state, the most
	// recent, and report more (older) remain.
	page1, err := requestHistory(ctx, conn, "campaign-page", "player-a", protocol.HistoryRequestPayload{Limit: 1})
	if err != nil {
		t.Fatalf("requestHistory(page1) error = %v", err)
	}
	if len(page1.Events) != 1 {
		t.Fatalf("page1: len(Events) = %d, want 1", len(page1.Events))
	}
	assertEventType(t, page1.Events[0], "system.session_state")
	if !page1.HasMore {
		t.Error("page1: HasMore = false, want true (system.connect is still older)")
	}

	// "Load earlier" from page1: should surface system.connect and
	// report nothing older remains.
	page2, err := requestHistory(ctx, conn, "campaign-page", "player-a", protocol.HistoryRequestPayload{
		BeforeSequence: page1.NextBeforeSequence,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("requestHistory(page2) error = %v", err)
	}
	if len(page2.Events) != 1 {
		t.Fatalf("page2: len(Events) = %d, want 1 (system.connect)", len(page2.Events))
	}
	assertEventType(t, page2.Events[0], "system.connect")
	if page2.HasMore {
		t.Error("page2: HasMore = true, want false (system.connect is the first event in the campaign)")
	}
}

func TestServe_LogHistoryRequest_AfterSequence_WalksForward(t *testing.T) {
	ts := newTestServerWithHistory(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-page", "player-a")
	defer conn.CloseNow()
	ctx := context.Background()

	// A big enough limit that the default/tail page captures everything
	// (2 events), giving a real anchor to page forward from.
	all, err := requestHistory(ctx, conn, "campaign-page", "player-a", protocol.HistoryRequestPayload{Limit: 100})
	if err != nil {
		t.Fatalf("requestHistory(all) error = %v", err)
	}
	if len(all.Events) != 2 {
		t.Fatalf("len(all.Events) = %d, want 2", len(all.Events))
	}
	assertEventType(t, all.Events[0], "system.connect")
	assertEventType(t, all.Events[1], "system.session_state")

	// all.NextBeforeSequence is the oldest event's own Sequence (i.e.
	// system.connect's) — a real anchor to page forward from, fetching
	// everything newer than it.
	page, err := requestHistory(ctx, conn, "campaign-page", "player-a", protocol.HistoryRequestPayload{
		AfterSequence: all.NextBeforeSequence,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("requestHistory(page) error = %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("len(page.Events) = %d, want 1 (system.session_state)", len(page.Events))
	}
	assertEventType(t, page.Events[0], "system.session_state")
	if page.HasMore {
		t.Error("HasMore = true, want false (system.session_state is the newest event)")
	}
}

// assertEventType unmarshals raw (one entry from a log.history_response's
// Events) as an Envelope and checks its Type.
func assertEventType(t *testing.T, raw json.RawMessage, wantType string) {
	t.Helper()
	var envelope protocol.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("Unmarshal(event) error = %v", err)
	}
	if string(envelope.Type) != wantType {
		t.Errorf("event.type = %q, want %q", envelope.Type, wantType)
	}
}

// sendSafetyFlagAndAwaitBroadcast sends a safety.flag with a unique
// message_id (messageID) on conn and reads back the resulting
// safety.flag_broadcast — for tests that just need some real, distinct
// traffic recorded on the campaign without caring about its content.
func sendSafetyFlagAndAwaitBroadcast(t *testing.T, conn *websocket.Conn, campaignID, sender, messageID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       messageID,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) error = %v", err)
	}
	var broadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &broadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) error = %v", err)
	}
}

func TestServe_LogHistoryRequest_PersistenceDisabled_RespondsWithError(t *testing.T) {
	ts := newTestServer(t) // uses server.New(logger, nil, nil, "", nil, nil, nil) — no store
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := requestHistory(ctx, conn, "campaign-1", "player-a", protocol.HistoryRequestPayload{})
	if err == nil {
		t.Fatal("requestHistory() succeeded, want a system.error response")
	}
}

func TestServe_LogHistoryRequest_ConflictingBounds_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	ts := newTestServerWithHistory(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()
	ctx := context.Background()

	_, err := requestHistory(ctx, conn, "campaign-1", "player-a", protocol.HistoryRequestPayload{
		AfterSequence:  1,
		BeforeSequence: 2,
	})
	if err == nil {
		t.Fatal("requestHistory() succeeded, want a system.error response (after_sequence and before_sequence are mutually exclusive)")
	}

	// Connection must still be usable afterward. 3, not 2: the rejection
	// above is itself a persisted system.error event (recordEvent runs
	// for every message sendError sends, same as any other rejection).
	page, err := requestHistory(ctx, conn, "campaign-1", "player-a", protocol.HistoryRequestPayload{})
	if err != nil {
		t.Fatalf("requestHistory() after conflicting-bounds error = %v", err)
	}
	if len(page.Events) != 3 {
		t.Errorf("len(page.Events) = %d, want 3 (connect, session_state, the rejection's system.error)", len(page.Events))
	}
}

// requestHistory sends a log.history_request on conn and returns either
// the log.history_response payload or an error built from a system.error
// response.
func requestHistory(ctx context.Context, conn *websocket.Conn, campaignID, sender string, payload protocol.HistoryRequestPayload) (protocol.HistoryResponsePayload, error) {
	req := protocol.HistoryRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "hist-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeLogHistoryRequest,
		},
		Payload: payload,
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		return protocol.HistoryResponsePayload{}, fmt.Errorf("writing request: %w", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return protocol.HistoryResponsePayload{}, fmt.Errorf("reading response: %w", err)
	}

	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return protocol.HistoryResponsePayload{}, fmt.Errorf("unmarshaling envelope: %w", err)
	}
	if envelope.Type == protocol.MessageTypeSystemError {
		var errMsg protocol.SystemErrorMessage
		if err := json.Unmarshal(data, &errMsg); err != nil {
			return protocol.HistoryResponsePayload{}, fmt.Errorf("unmarshaling system.error: %w", err)
		}
		return protocol.HistoryResponsePayload{}, fmt.Errorf("server rejected request: %s", errMsg.Payload.Message)
	}

	var resp protocol.HistoryResponseMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return protocol.HistoryResponsePayload{}, fmt.Errorf("unmarshaling log.history_response: %w", err)
	}
	return resp.Payload, nil
}

func TestServe_NarrativePlayerInput_RendersAndBroadcastsBubble(t *testing.T) {
	fake := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Player-A draws a sword."}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, fake, "test-model", nil, nil, nil).Handler())
	defer ts.Close()

	a := dialAndJoin(t, ts, "campaign-narrative", "player-a")
	defer a.CloseNow()
	b := dialAndJoin(t, ts, "campaign-narrative", "player-b")
	defer b.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := protocol.NarrativePlayerInputMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "input-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-narrative",
			Type:            protocol.MessageTypeNarrativePlayerInput,
		},
		Payload: protocol.NarrativePlayerInputPayload{
			CharacterID: "char-a",
			Text:        "I draw my sword.",
			Source:      protocol.NarrativeInputSourceTyped,
		},
	}
	if err := wsjson.Write(ctx, a, input); err != nil {
		t.Fatalf("Write(narrative.player_input) error = %v", err)
	}

	for name, conn := range map[string]*websocket.Conn{"player-a (sender)": a, "player-b": b} {
		var got protocol.NarrativePlayerBubbleMessage
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("%s: Read(narrative.player_bubble) error = %v", name, err)
		}
		if got.Type != protocol.MessageTypeNarrativePlayerBubble {
			t.Errorf("%s: Type = %q, want %q", name, got.Type, protocol.MessageTypeNarrativePlayerBubble)
		}
		if got.Payload.Text != "Player-A draws a sword." {
			t.Errorf("%s: Payload.Text = %q, want %q", name, got.Payload.Text, "Player-A draws a sword.")
		}
		if got.Payload.CharacterID != "char-a" {
			t.Errorf("%s: Payload.CharacterID = %q, want %q", name, got.Payload.CharacterID, "char-a")
		}
		if !got.Payload.Editable {
			t.Errorf("%s: Payload.Editable = false, want true", name)
		}
	}

	if fake.lastRequest.Model != "test-model" {
		t.Errorf("Complete() called with Model = %q, want %q", fake.lastRequest.Model, "test-model")
	}
	if fake.lastRequest.UserPrompt != "I draw my sword." {
		t.Errorf("Complete() called with UserPrompt = %q, want %q", fake.lastRequest.UserPrompt, "I draw my sword.")
	}
}

func TestServe_NarrativePlayerInput_NoProvider_RespondsWithError(t *testing.T) {
	ts := newTestServer(t) // no LLM provider configured
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := protocol.NarrativePlayerInputMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "input-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeNarrativePlayerInput,
		},
		Payload: protocol.NarrativePlayerInputPayload{CharacterID: "char-a", Text: "hi", Source: protocol.NarrativeInputSourceTyped},
	}
	if err := wsjson.Write(ctx, conn, input); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.InReplyToMessageID != input.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", got.Payload.InReplyToMessageID, input.MessageID)
	}
}

func TestServe_NarrativePlayerInput_ProviderError_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	fake := &fakeLLMProvider{err: errors.New("model unavailable")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, fake, "test-model", nil, nil, nil).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := protocol.NarrativePlayerInputMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "input-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeNarrativePlayerInput,
		},
		Payload: protocol.NarrativePlayerInputPayload{CharacterID: "char-a", Text: "hi", Source: protocol.NarrativeInputSourceTyped},
	}
	if err := wsjson.Write(ctx, conn, input); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var gotErr protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &gotErr); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}

	// Connection must still be usable afterward.
	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-after-llm-error",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) after LLM error error = %v", err)
	}
	var gotBroadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &gotBroadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) after LLM error error = %v", err)
	}
}

// fakeLLMProvider is a minimal llm.Provider for testing narrative
// rendering without a real Ollama server.
type fakeLLMProvider struct {
	response llm.CompletionResponse
	err      error
	// lastRequest captures the most recent Complete() call's request,
	// for asserting on what Server actually sent to the provider.
	lastRequest llm.CompletionRequest
}

func (f *fakeLLMProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	f.lastRequest = req
	if f.err != nil {
		return llm.CompletionResponse{}, f.err
	}
	return f.response, nil
}

// dialAndJoin dials ts, completes the handshake for campaignID
// (attributing the connect to sender, used as both message_id prefix and
// sender_id so failures are easy to trace back to a specific test
// client), and returns the open connection positioned right after
// reading the session_state response.
func dialAndJoin(t *testing.T, ts *httptest.Server, campaignID, sender string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       sender + "-connect",
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "test_client"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var joined protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &joined); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}
	if joined.Payload.State != protocol.SessionStateJoined {
		t.Fatalf("Payload.State = %q, want %q", joined.Payload.State, protocol.SessionStateJoined)
	}
	return conn
}

func TestHandleWebSocket_AuthProvider_CorrectPassword_Joins(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authProvider := auth.NewRoomPasswordProvider(map[string]string{"protected-campaign": "hunter2"})
	ts := httptest.NewServer(server.New(logger, nil, nil, "", authProvider, nil, nil).Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "protected-campaign",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "test_client", AuthToken: "hunter2"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var got protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}
	if got.Payload.State != protocol.SessionStateJoined {
		t.Errorf("Payload.State = %q, want %q", got.Payload.State, protocol.SessionStateJoined)
	}
}

func TestHandleWebSocket_AuthProvider_WrongPassword_RejectsHandshake(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authProvider := auth.NewRoomPasswordProvider(map[string]string{"protected-campaign": "hunter2"})
	ts := httptest.NewServer(server.New(logger, nil, nil, "", authProvider, nil, nil).Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.CloseNow()

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-2",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "protected-campaign",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "test_client", AuthToken: "wrong-password"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.InReplyToMessageID != connect.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", got.Payload.InReplyToMessageID, connect.MessageID)
	}
	if got.Payload.Code != "handshake_rejected" {
		t.Errorf("Payload.Code = %q, want %q", got.Payload.Code, "handshake_rejected")
	}

	// The handshake gate is a hard reject-and-close, unlike a rejected
	// post-handshake message — confirm the connection actually closed
	// rather than staying open for a retry on the same connection.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()
	if _, _, err := conn.Read(shortCtx); err == nil {
		t.Error("connection stayed open after a rejected handshake, want it closed")
	}
}

func TestHandleWebSocket_AuthProvider_UnconfiguredCampaign_JoinsWithoutPassword(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Auth is enabled server-wide, but this specific campaign has no
	// password configured — per-campaign opt-in, not an all-or-nothing
	// switch.
	authProvider := auth.NewRoomPasswordProvider(map[string]string{"protected-campaign": "hunter2"})
	ts := httptest.NewServer(server.New(logger, nil, nil, "", authProvider, nil, nil).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "public-campaign", "player-a")
	defer conn.CloseNow()
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, nil, nil, "", nil, nil, nil)
	return httptest.NewServer(srv.Handler())
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
