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

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, nil)
	return httptest.NewServer(srv.Handler())
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
