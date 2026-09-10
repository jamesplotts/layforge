// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"testing"

	"github.com/coder/websocket"
)

// newPromptWaiter builds a promptWaiter whose deliver callback records the
// answer it was handed into *got.
func newPromptWaiter(senderID string, kind promptKind, got *string) promptWaiter {
	return promptWaiter{
		senderID: senderID,
		kind:     kind,
		deliver: func(_ context.Context, _ *websocket.Conn, answer string) error {
			*got = answer
			return nil
		},
	}
}

// TestPendingPrompts_ResolveIsSingleUse checks the registry hands a
// waiter back exactly once and forgets it, so a stale or duplicate
// response finds nothing.
func TestPendingPrompts_ResolveIsSingleUse(t *testing.T) {
	s := &Server{pendingPrompts: make(map[string]promptWaiter)}
	var got string
	s.registerPrompt("p-1", newPromptWaiter("player-a", promptKindQuery, &got))

	w, ok := s.resolvePrompt("p-1")
	if !ok {
		t.Fatal("resolvePrompt(p-1) ok = false, want true")
	}
	if w.senderID != "player-a" || w.kind != promptKindQuery {
		t.Errorf("resolved waiter = %+v", w)
	}
	if _, ok := s.resolvePrompt("p-1"); ok {
		t.Error("resolvePrompt(p-1) second call ok = true, want false")
	}
}

// TestPendingPrompts_Discard drops a waiter without resolving it.
func TestPendingPrompts_Discard(t *testing.T) {
	s := &Server{pendingPrompts: make(map[string]promptWaiter)}
	var got string
	s.registerPrompt("p-1", newPromptWaiter("player-a", promptKindChoice, &got))
	s.discardPrompt("p-1")
	if _, ok := s.resolvePrompt("p-1"); ok {
		t.Error("resolvePrompt after discard ok = true, want false")
	}
}

// TestDeliverPromptAnswer_HappyPath routes a matching response to the
// registered deliver callback and consumes the prompt. (The rejection
// paths — unknown id, wrong sender, wrong kind — send a system.error on
// the connection and are covered end-to-end by the character-creation
// tests, which drive a real websocket.)
func TestDeliverPromptAnswer_HappyPath(t *testing.T) {
	s := &Server{pendingPrompts: make(map[string]promptWaiter)}
	var got string
	s.registerPrompt("p-1", newPromptWaiter("player-a", promptKindQuery, &got))

	if err := s.deliverPromptAnswer(context.Background(), nil, "campaign-1", "player-a", "in-reply", "p-1", promptKindQuery, "Reorx"); err != nil {
		t.Fatalf("deliverPromptAnswer() error = %v", err)
	}
	if got != "Reorx" {
		t.Errorf("deliver got %q, want %q", got, "Reorx")
	}
	if _, ok := s.resolvePrompt("p-1"); ok {
		t.Error("prompt still registered after a successful delivery")
	}
}
