// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

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
	"github.com/jamesplotts/layforge/master/internal/session"
)

// TestServe_HubKick_ClosesRealConnection is a white-box test (package
// server, not server_test) because it needs the same *session.Hub
// instance passed into New — proving Hub.Kick, called from outside this
// package exactly as the admin panel's kick endpoint calls it, actually
// tears down a real websocket connection via the closer serve()
// attaches, not just a fake session.Client in isolation (see
// internal/session/hub_test.go for that narrower unit coverage).
func TestServe_HubKick_ClosesRealConnection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := session.NewHub()
	srv := New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
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
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "player_web_v1"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}
	var joined protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &joined); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}

	// Give serve() a moment to reach its blocking conn.Read before we
	// kick — Register (and therefore SetCloser) has already happened by
	// the time the session_state reply above was sent, so this is just
	// to let the read loop settle into Read.
	time.Sleep(50 * time.Millisecond)

	if !hub.Kick("campaign-1", "player-a") {
		t.Fatal("Kick() = false, want true for the just-connected client")
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	if _, _, err := conn.Read(readCtx); err == nil {
		t.Fatal("Read() after Kick() succeeded, want the connection to have been closed")
	}
}

// TestServe_AfterDisconnect_HubUnregistersPromptly is a regression test
// for a real deadlock live-testing this package's Hub.Kick support
// found: serve() used to defer Hub.Unregister until its own return, but
// serve() can't return until writePump reports back on writeDone, and
// writePump's own idle range over an empty Outbox() never notices a
// dead connection unless something is queued to it — so readLoop
// ending was not, by itself, enough to ever unblock any of it. A
// connected sender_id would then stay listed as connected
// (Hub.ConnectedSenders, and by extension the admin panel's kick UI)
// indefinitely after actually disconnecting, cleanly or via Kick alike
// — this asserts the connection actually clears out of the Hub, not
// just that the read failed.
func TestServe_AfterDisconnect_HubUnregistersPromptly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := session.NewHub()
	srv := New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "test-msg-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "player_web_v1"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}
	var joined protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &joined); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}

	conn.CloseNow() // a normal client-initiated disconnect, not Kick

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if senders := hub.ConnectedSenders("campaign-1"); len(senders) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("player-a still listed as connected 2s after disconnecting — Hub.Unregister never ran")
}
