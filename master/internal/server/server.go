// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package server implements Master's client-facing WebSocket endpoint:
// accepting connections and running the protocol handshake
// (system.connect -> system.session_state, or system.error on
// rejection). See design doc §5 for the protocol and §11 for what's
// still to come — only the handshake is implemented so far; there is no
// session orchestration, tool-use dispatch, or governance-gate
// enforcement yet.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// masterSenderID is the sender_id Master uses on messages it originates
// during the handshake, before any richer per-connection identity exists.
const masterSenderID = "master"

// Server serves Master's client-facing WebSocket endpoint.
type Server struct {
	logger *slog.Logger
	events store.EventStore
}

// New creates a Server. logger must not be nil; pass slog.Default() if
// the caller has no specific logger configured. events may be nil to run
// without persistence (e.g. in tests that don't care about the audit
// log) — every message Server exchanges is still handled normally either
// way, since recording is best-effort (see recordEvent).
func New(logger *slog.Logger, events store.EventStore) *Server {
	return &Server{logger: logger, events: events}
}

// Handler returns the http.Handler that serves the WebSocket endpoint,
// suitable for mounting at any path (e.g. "/ws") on an http.ServeMux.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handleWebSocket)
}

// handleWebSocket upgrades r to a WebSocket connection and runs the
// protocol handshake on it. Errors are logged, not returned — an
// http.Handler has no caller to report them to once the upgrade
// succeeds.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Error("websocket accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	if err := s.handleConnection(r.Context(), conn); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("connection ended with error", "error", err)
	}
}

// handleConnection runs one client connection's protocol handshake. A
// panic while handling a single connection (e.g. from a pathological
// message) is recovered here so it can't take Master down for every
// other player at the table — see CLAUDE.md's error-handling
// conventions for why this is the one place a recover() belongs.
func (s *Server) handleConnection(ctx context.Context, conn *websocket.Conn) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic handling connection: %v", r)
		}
	}()

	var connect protocol.SystemConnectMessage
	if err := wsjson.Read(ctx, conn, &connect); err != nil {
		return fmt.Errorf("reading handshake: %w", err)
	}
	recordEvent(ctx, s, connect)

	campaignID := connect.CampaignID
	if campaignID == "" {
		campaignID = "unknown"
	}

	if verr := connect.Envelope.Validate(); verr != nil {
		return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID, verr)
	}
	if connect.Type != protocol.MessageTypeSystemConnect {
		return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
			fmt.Errorf("first message must be %q, got %q", protocol.MessageTypeSystemConnect, connect.Type))
	}

	s.logger.Info("client connected",
		"client_kind", connect.Payload.ClientKind,
		"campaign_id", connect.CampaignID,
	)

	return s.sendSessionState(ctx, conn, campaignID, protocol.SessionStateJoined)
}

// sendSessionState sends a system.session_state message to conn and, for
// V0's handshake-only implementation, closes the connection normally
// afterward — there is nothing else implemented for it to do yet.
func (s *Server) sendSessionState(ctx context.Context, conn *websocket.Conn, campaignID string, state protocol.SessionState) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemSessionState, protocol.SystemSessionStatePayload{
		State: state,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing session_state: %w", err)
	}

	conn.Close(websocket.StatusNormalClosure, "handshake complete; nothing else implemented yet")
	return nil
}

// rejectHandshake sends a system.error message explaining why the
// handshake failed, then closes the connection with a policy-violation
// status. It returns cause so the caller's error path reports the same
// failure it just told the client about.
func (s *Server) rejectHandshake(ctx context.Context, conn *websocket.Conn, inReplyTo, campaignID string, cause error) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemError, protocol.SystemErrorPayload{
		Code:               "handshake_rejected",
		Message:            cause.Error(),
		InReplyToMessageID: inReplyTo,
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	recordEvent(ctx, s, msg)
	// Best-effort: the connection is closing regardless of whether the
	// client receives this, so a write failure here doesn't change the
	// outcome — it only means the client won't know why.
	if werr := wsjson.Write(ctx, conn, msg); werr != nil {
		s.logger.Warn("failed to send handshake rejection", "error", werr)
	}
	conn.Close(websocket.StatusPolicyViolation, "handshake rejected")
	return cause
}

// recordEvent best-effort persists msg to s's event log: a failure here
// is logged, not returned, because losing an audit-log entry shouldn't
// interrupt the live connection that's the actual point of Server. This
// is deliberately different from how design doc §10 describes
// authoritative session/character state, which does need durability
// guarantees — but that state doesn't exist yet (only the handshake
// does), so today this records handshake traffic as an audit trail, not
// authoritative game state. It is a free function taking *Server rather
// than a Server method for the same reason newMessage is: Go methods
// cannot themselves be generic.
func recordEvent[T any](ctx context.Context, s *Server, msg protocol.Message[T]) {
	if s.events == nil {
		return
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		s.logger.Warn("failed to marshal event for persistence", "error", err, "message_id", msg.MessageID)
		return
	}

	err = s.events.AppendEvent(ctx, store.Event{
		CampaignID:  msg.CampaignID,
		MessageID:   msg.MessageID,
		MessageType: string(msg.Type),
		SenderID:    msg.SenderID,
		OccurredAt:  msg.Timestamp,
		Raw:         raw,
	})
	if err != nil && !errors.Is(err, store.ErrDuplicateMessage) {
		s.logger.Warn("failed to persist event", "error", err, "message_id", msg.MessageID)
	}
}

// newMessage builds a Message[T] envelope for a Master-originated
// message: current protocol version, a fresh message_id, current
// timestamp, and Master's sender_id. It is a free function rather than a
// Server method because Go methods cannot themselves be generic; callers
// still read naturally as newMessage(campaignID, type, payload).
func newMessage[T any](campaignID string, msgType protocol.MessageType, payload T) (protocol.Message[T], error) {
	id, err := newMessageID()
	if err != nil {
		return protocol.Message[T]{}, err
	}
	return protocol.Message[T]{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       id,
			Timestamp:       time.Now().UTC(),
			SenderID:        masterSenderID,
			CampaignID:      campaignID,
			Type:            msgType,
		},
		Payload: payload,
	}, nil
}
