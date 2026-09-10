// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// The client.* family (design doc §4) — the reusable chat-bubble
// interactions Master uses to talk to a player. This file holds the send
// helpers and the pending-prompt registry that pairs a client.query /
// client.choice with its eventual response. Character creation
// (character_creation.go) is built entirely on top of these.

// promptKind distinguishes a pending client.query from a client.choice so
// a response of the wrong type is rejected rather than silently accepted.
type promptKind int

const (
	promptKindQuery promptKind = iota
	promptKindChoice
)

// promptWaiter is what a pending prompt_id resolves to: who is allowed to
// answer it, whether it's a query or a choice, and the callback that
// consumes the answer (typically a character-creation stage handler).
type promptWaiter struct {
	senderID string
	kind     promptKind
	deliver  func(ctx context.Context, conn *websocket.Conn, answer string) error
}

// registerPrompt records a pending prompt. The caller has already sent
// the client.query / client.choice with this prompt_id.
func (s *Server) registerPrompt(promptID string, w promptWaiter) {
	s.pendingPromptsMu.Lock()
	defer s.pendingPromptsMu.Unlock()
	s.pendingPrompts[promptID] = w
}

// resolvePrompt looks up and removes a pending prompt — single-use, so a
// duplicate or stale response finds nothing.
func (s *Server) resolvePrompt(promptID string) (promptWaiter, bool) {
	s.pendingPromptsMu.Lock()
	defer s.pendingPromptsMu.Unlock()
	w, ok := s.pendingPrompts[promptID]
	if ok {
		delete(s.pendingPrompts, promptID)
	}
	return w, ok
}

// discardPrompt drops a pending prompt without resolving it — used when a
// flow supersedes its own earlier prompt (creation moving to the next
// step) or abandons one on error.
func (s *Server) discardPrompt(promptID string) {
	s.pendingPromptsMu.Lock()
	defer s.pendingPromptsMu.Unlock()
	delete(s.pendingPrompts, promptID)
}

// sendClientQuery writes a client.query on conn and returns the
// prompt_id it was sent with (generated here if p.PromptID is empty).
// The caller registers a promptWaiter for that id.
func (s *Server) sendClientQuery(ctx context.Context, conn *websocket.Conn, campaignID string, p protocol.ClientQueryPayload) (string, error) {
	if p.PromptID == "" {
		id, err := newRandomID()
		if err != nil {
			return "", err
		}
		p.PromptID = id
	}
	msg, err := newMessage(campaignID, protocol.MessageTypeClientQuery, p)
	if err != nil {
		return "", err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return "", fmt.Errorf("writing client.query: %w", err)
	}
	return p.PromptID, nil
}

// sendClientChoice writes a client.choice on conn and returns the
// prompt_id it was sent with (generated here if p.PromptID is empty).
func (s *Server) sendClientChoice(ctx context.Context, conn *websocket.Conn, campaignID string, p protocol.ClientChoicePayload) (string, error) {
	if p.PromptID == "" {
		id, err := newRandomID()
		if err != nil {
			return "", err
		}
		p.PromptID = id
	}
	msg, err := newMessage(campaignID, protocol.MessageTypeClientChoice, p)
	if err != nil {
		return "", err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return "", fmt.Errorf("writing client.choice: %w", err)
	}
	return p.PromptID, nil
}

// sendClientDisplay sends a client.display: broadcast to the whole
// campaign when recipient is empty (and recorded to the event log), or
// to one connection only when it names a sender_id/account (per-player
// narration, design doc §9.7 — not recorded, same reasoning as
// sendToSender's own doc comment).
func (s *Server) sendClientDisplay(ctx context.Context, campaignID, recipient, text, inReplyTo string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeClientDisplay, protocol.ClientDisplayPayload{
		Recipient:          recipient,
		Text:               text,
		InReplyToMessageID: inReplyTo,
	})
	if err != nil {
		return err
	}
	if recipient == "" {
		recordEvent(ctx, s, msg)
		return broadcastMessage(s, msg)
	}
	return sendToSender(s, recipient, msg)
}

// clientImageArgs is sendClientImage's input.
type clientImageArgs struct {
	Recipient          string
	ImageURL           string
	Caption            string
	Prompt             string
	InReplyToMessageID string
}

// sendClientImage sends a client.image, with the same broadcast-vs-
// targeted semantics as sendClientDisplay.
func (s *Server) sendClientImage(ctx context.Context, campaignID string, a clientImageArgs) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeClientImage, protocol.ClientImagePayload{
		Recipient:          a.Recipient,
		ImageURL:           a.ImageURL,
		Caption:            a.Caption,
		Prompt:             a.Prompt,
		InReplyToMessageID: a.InReplyToMessageID,
	})
	if err != nil {
		return err
	}
	if a.Recipient == "" {
		recordEvent(ctx, s, msg)
		return broadcastMessage(s, msg)
	}
	return sendToSender(s, a.Recipient, msg)
}

// handleClientQueryResponse / handleClientChoiceResponse are dispatch's
// entry points for the two response messages. Both look up the pending
// prompt, check it belongs to this sender and is the right kind, and
// hand the answer to the registered callback.
func (s *Server) handleClientQueryResponse(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.ClientQueryResponseMessage) error {
	return s.deliverPromptAnswer(ctx, conn, campaignID, senderID, req.MessageID, req.Payload.PromptID, promptKindQuery, req.Payload.Text)
}

func (s *Server) handleClientChoiceResponse(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.ClientChoiceResponseMessage) error {
	return s.deliverPromptAnswer(ctx, conn, campaignID, senderID, req.MessageID, req.Payload.PromptID, promptKindChoice, req.Payload.Value)
}

func (s *Server) deliverPromptAnswer(ctx context.Context, conn *websocket.Conn, campaignID, senderID, inReplyTo, promptID string, kind promptKind, answer string) error {
	if promptID == "" {
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("prompt_id is required"))
	}
	w, ok := s.resolvePrompt(promptID)
	if !ok {
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("no prompt is waiting for that prompt_id (it may have been answered already, or expired)"))
	}
	if w.senderID != senderID {
		// Someone answering a prompt that wasn't theirs. Put it back so
		// the real recipient can still answer.
		s.registerPrompt(promptID, w)
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("that prompt was not addressed to you"))
	}
	if w.kind != kind {
		s.registerPrompt(promptID, w)
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("wrong response type for that prompt"))
	}
	return w.deliver(ctx, conn, answer)
}
