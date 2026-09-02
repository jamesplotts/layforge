// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package server implements Master's client-facing WebSocket endpoint:
// the protocol handshake (system.connect, optionally gated by package
// auth -> system.session_state, or system.error on rejection), followed
// by a per-connection message loop that dispatches whatever the client
// sends next. See design doc §5 for the protocol and §11 for what's
// still to come — governance-gate enforcement and most message
// categories (roll, map, character, tool) don't exist yet. Implemented
// so far: safety.flag (§9.2), broadcast to every client in the campaign
// via package session's Hub; log.history_request (§10, §11), answered
// from package store directly to the requester; and
// narrative.player_input (§7's fast pass only — see renderPlayerBubble),
// rendered via package llm and broadcast as narrative.player_bubble. See
// CLAUDE.md and each dispatch case's own comments for why a given
// message either is or isn't implemented yet.
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

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// masterSenderID is the sender_id Master uses on messages it originates
// (session-state notifications, errors, and broadcasts), since none of
// them are attributed to a specific human sender — see
// broadcastSafetyFlag for why that matters for safety.flag specifically.
const masterSenderID = "master"

// Server serves Master's client-facing WebSocket endpoint.
type Server struct {
	logger *slog.Logger
	events store.EventStore
	hub    *session.Hub
	auth   auth.Provider

	llm            llm.Provider
	narrativeModel string

	systemEngine systemenginepb.SystemEngineClient
}

// New creates a Server. logger must not be nil; pass slog.Default() if
// the caller has no specific logger configured. events may be nil to run
// without persistence (e.g. in tests that don't care about the audit
// log) — every message Server exchanges is still handled normally either
// way, since recording is best-effort (see recordEvent). llmProvider may
// also be nil to run without narrative rendering — a
// narrative.player_input then gets a system.error explaining rendering
// is unavailable, rather than Master panicking on a nil provider.
// narrativeModel names the model llmProvider should use; it's ignored
// when llmProvider is nil. authProvider may be nil to run with no join
// authorization at all (every campaign open to anyone, today's default);
// design doc §6.6 — a future Discord-OAuth-backed auth.Provider is meant
// to plug into this same field. systemEngineClient may be nil to run
// without a System Engine sidecar configured at all (design doc §6.1) —
// nothing in Server calls it yet (dice/rules dispatch is design doc §11
// future work), but it is wired in now so that dispatch code has
// somewhere real to call once it exists, rather than needing a second
// plumbing change later.
func New(logger *slog.Logger, events store.EventStore, llmProvider llm.Provider, narrativeModel string, authProvider auth.Provider, systemEngineClient systemenginepb.SystemEngineClient) *Server {
	return &Server{
		logger:         logger,
		events:         events,
		hub:            session.NewHub(),
		llm:            llmProvider,
		narrativeModel: narrativeModel,
		auth:           authProvider,
		systemEngine:   systemEngineClient,
	}
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

// handleConnection runs one client connection: the protocol handshake,
// then (if it succeeds) the post-handshake message loop in serve. A
// panic anywhere in that call chain (e.g. from a pathological message)
// is recovered here so it can't take Master down for every other player
// at the table — see CLAUDE.md's error-handling conventions for why this
// is the one place a recover() belongs. Note this only covers the
// goroutine handleConnection itself runs on; serve's write pump runs on
// its own goroutine and recovers separately (see serve).
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

	if s.auth != nil {
		authorized, reason, authErr := s.auth.Authorize(ctx, campaignID, connect.Payload.AuthToken)
		if authErr != nil {
			return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
				fmt.Errorf("checking authorization: %w", authErr))
		}
		if !authorized {
			return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
				fmt.Errorf("not authorized to join this campaign: %s", reason))
		}
	}

	s.logger.Info("client connected",
		"client_kind", connect.Payload.ClientKind,
		"campaign_id", connect.CampaignID,
	)

	if err := s.sendSessionState(ctx, conn, campaignID, protocol.SessionStateJoined); err != nil {
		return err
	}

	return s.serve(ctx, conn, campaignID)
}

// sendSessionState sends a system.session_state message to conn.
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
	return nil
}

// serve runs the post-handshake message loop for a joined connection: a
// write pump delivering session.Hub broadcasts to this client, and a
// read loop dispatching each inbound message by type. It returns once
// the connection ends, for any reason — client disconnect, a read/write
// error, or ctx cancellation.
func (s *Server) serve(ctx context.Context, conn *websocket.Conn, campaignID string) error {
	client := s.hub.Register(campaignID)
	defer s.hub.Unregister(client)

	writeDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeDone <- fmt.Errorf("recovered from panic in write pump: %v", r)
			}
		}()
		writeDone <- s.writePump(ctx, conn, client)
	}()

	readErr := s.readLoop(ctx, conn, campaignID)

	// Unregister (above, via defer) closes client's outbox once serve
	// returns, which ends writePump's range loop — but that hasn't run
	// yet at this point, so wait for it now rather than returning (and
	// letting the caller close conn) while it might still be writing.
	writeErr := <-writeDone

	if readErr != nil {
		return readErr
	}
	return writeErr
}

// writePump delivers every message sent to client's outbox to conn,
// until the outbox is closed (by Hub.Unregister) or a write fails.
func (s *Server) writePump(ctx context.Context, conn *websocket.Conn, client *session.Client) error {
	for payload := range client.Outbox() {
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return fmt.Errorf("writing broadcast message: %w", err)
		}
	}
	return nil
}

// readLoop reads messages from conn until one read fails (including the
// client disconnecting, or ctx being canceled), dispatching each to
// dispatch. dispatch itself only returns an error for a genuine
// transport failure while responding to the sender — a message that's
// merely malformed or unsupported gets a system.error reply and the loop
// continues, so one bad message doesn't end the connection.
func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn, campaignID string) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading message: %w", err)
		}
		if err := s.dispatch(ctx, conn, campaignID, data); err != nil {
			return err
		}
	}
}

// dispatch decodes one inbound message and routes it by type.
func (s *Server) dispatch(ctx context.Context, conn *websocket.Conn, campaignID string, data []byte) error {
	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("malformed message: %w", err))
	}
	if verr := envelope.Validate(); verr != nil {
		return s.sendError(ctx, conn, campaignID, envelope.MessageID, verr)
	}

	switch envelope.Type {
	case protocol.MessageTypeSafetyFlag:
		var flag protocol.SafetyFlagMessage
		if err := json.Unmarshal(data, &flag); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed safety.flag payload: %w", err))
		}
		recordEvent(ctx, s, flag)
		return s.broadcastSafetyFlag(ctx, campaignID, flag.Payload.Topic)
	case protocol.MessageTypeLogHistoryRequest:
		var req protocol.HistoryRequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed log.history_request payload: %w", err))
		}
		// Not recorded to the event log: this is a query against the
		// log, not a game event — recording it would just be recursive
		// noise (a request to view history, sitting in the history).
		return s.sendHistory(ctx, conn, campaignID, envelope.MessageID, req.Payload)
	case protocol.MessageTypeNarrativePlayerInput:
		var input protocol.NarrativePlayerInputMessage
		if err := json.Unmarshal(data, &input); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed narrative.player_input payload: %w", err))
		}
		recordEvent(ctx, s, input)
		return s.renderPlayerBubble(ctx, conn, campaignID, input)
	default:
		return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("unsupported message type %q", envelope.Type))
	}
}

// broadcastSafetyFlag builds and persists a safety.flag_broadcast
// message and delivers it to every client currently registered under
// campaignID — deliberately including whichever client sent the
// triggering safety.flag, since design doc §9.2 wants the scene
// interrupted for everyone at the table, sender included, and
// deliberately not naming who sent it: the broadcast is attributed to
// masterSenderID like every other Master-originated message, not to the
// flagging client.
func (s *Server) broadcastSafetyFlag(ctx context.Context, campaignID, topic string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSafetyFlagBroadcast, protocol.SafetyFlagBroadcastPayload{
		Topic: topic,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling safety.flag_broadcast: %w", err)
	}
	s.hub.Broadcast(campaignID, payload)
	return nil
}

// narrativeFastPassSystemPrompt instructs the model for design doc §7's
// fast pass: render the player's stated action/dialogue in third-person
// prose, faithfully — not the DM/NPC reaction, and not a ruling on
// whether the action succeeds. That's the slow pass's job (design doc
// §7's second beat), which isn't implemented — see renderPlayerBubble.
const narrativeFastPassSystemPrompt = `You are rendering a tabletop RPG player's stated action or dialogue into brief, third-person, present-tense narrative prose for a shared chat log.
Rules:
- Describe only what the player explicitly stated — do not invent new events, dialogue, or outcomes.
- Do not resolve success or failure of any action; that is decided elsewhere.
- Keep it to 1-3 sentences.
- Write in the tone of a dungeon master narrating at the table.
- Output only the narrated prose, nothing else — no preamble, no quotation marks around it.`

// renderPlayerBubble runs the narrative-transform pipeline's fast pass
// only (design doc §7): rendering the player's stated action/dialogue in
// third-person DM-voiced prose via s.llm, then broadcasting the result as
// narrative.player_bubble to everyone in the campaign. There is no slow
// pass yet — no DM/NPC reaction is generated, and no campaign/character
// context is fed to the model beyond the player's own input, since
// neither exists in Master yet to feed it.
func (s *Server) renderPlayerBubble(ctx context.Context, conn *websocket.Conn, campaignID string, input protocol.NarrativePlayerInputMessage) error {
	if s.llm == nil {
		return s.sendError(ctx, conn, campaignID, input.MessageID, errors.New("narrative rendering unavailable: no LLM provider configured"))
	}

	completion, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Model:        s.narrativeModel,
		SystemPrompt: narrativeFastPassSystemPrompt,
		UserPrompt:   input.Payload.Text,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, input.MessageID, fmt.Errorf("rendering narrative: %w", err))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeNarrativePlayerBubble, protocol.NarrativePlayerBubblePayload{
		CharacterID: input.Payload.CharacterID,
		Text:        completion.Text,
		Editable:    true,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling narrative.player_bubble: %w", err)
	}
	s.hub.Broadcast(campaignID, payload)
	return nil
}

// sendHistory answers a log.history_request with a page of campaign's
// recorded events (design doc §10, §11), sent only to the requesting
// connection — history is per-viewer to ask for, not broadcast. It
// rejects the request (via sendError) if persistence is disabled, since
// silently returning an empty page would look identical to "this
// campaign really has no history" rather than "this feature is off".
func (s *Server) sendHistory(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string, req protocol.HistoryRequestPayload) error {
	if s.events == nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("history unavailable: persistence is disabled"))
	}
	if req.AfterSequence != 0 && req.BeforeSequence != 0 {
		return s.sendError(ctx, conn, campaignID, inReplyTo, store.ErrConflictingPagination)
	}

	events, hasMore, err := s.events.ListEvents(ctx, campaignID, store.ListEventsOptions{
		AfterSequence:  req.AfterSequence,
		BeforeSequence: req.BeforeSequence,
		Limit:          req.Limit,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("fetching history: %w", err))
	}

	raw := make([]json.RawMessage, len(events))
	var oldest, newest int64
	if len(events) > 0 {
		// events is always oldest-first regardless of paging direction
		// (EventStore.ListEvents's contract), so the cursors are just
		// the endpoints — no need to scan for min/max.
		oldest = events[0].Sequence
		newest = events[len(events)-1].Sequence
	}
	for i, e := range events {
		raw[i] = e.Raw
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeLogHistoryResponse, protocol.HistoryResponsePayload{
		Events:             raw,
		NextBeforeSequence: oldest,
		NextAfterSequence:  newest,
		HasMore:            hasMore,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing log.history_response: %w", err)
	}
	return nil
}

// sendError sends a system.error message to conn — the sender only, not
// a broadcast — explaining why their message was rejected, then returns
// nil so the read loop continues. It only returns a non-nil error if
// writing the rejection itself fails, since that indicates a real
// transport problem rather than a client mistake.
func (s *Server) sendError(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string, cause error) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemError, protocol.SystemErrorPayload{
		Code:               "message_rejected",
		Message:            cause.Error(),
		InReplyToMessageID: inReplyTo,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing rejection: %w", err)
	}
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
