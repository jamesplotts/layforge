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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

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

	srv := server.New(logger, events)
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
	recorded, err := events.ListEvents(ctx, "campaign-persist", store.ListEventsOptions{})
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

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, nil)
	return httptest.NewServer(srv.Handler())
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
