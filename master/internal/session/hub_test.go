// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package session_test

import (
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/session"
)

// recvOrTimeout returns the next value from ch, failing the test if none
// arrives within a bounded window — used both for positive assertions
// ("this must arrive") and, with a short timeout, negative ones ("this
// must not").
func recvOrTimeout(t *testing.T, ch <-chan []byte, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(timeout):
		return nil, false
	}
}

func TestHub_Broadcast_DeliversToAllClientsInCampaign(t *testing.T) {
	h := session.NewHub()
	a := h.Register("campaign-1", "player-a")
	b := h.Register("campaign-1", "player-b")

	h.Broadcast("campaign-1", []byte("hello"))

	for name, c := range map[string]*session.Client{"a": a, "b": b} {
		got, ok := recvOrTimeout(t, c.Outbox(), time.Second)
		if !ok {
			t.Fatalf("client %s: no message received", name)
		}
		if string(got) != "hello" {
			t.Errorf("client %s: got %q, want %q", name, got, "hello")
		}
	}
}

func TestHub_Broadcast_DoesNotCrossCampaigns(t *testing.T) {
	h := session.NewHub()
	inCampaign1 := h.Register("campaign-1", "player-a")
	inCampaign2 := h.Register("campaign-2", "player-a")

	h.Broadcast("campaign-1", []byte("hello"))

	if _, ok := recvOrTimeout(t, inCampaign1.Outbox(), time.Second); !ok {
		t.Fatal("campaign-1 client: no message received")
	}
	if got, ok := recvOrTimeout(t, inCampaign2.Outbox(), 200*time.Millisecond); ok {
		t.Errorf("campaign-2 client: received %q, want nothing", got)
	}
}

func TestHub_Broadcast_UnknownCampaign_NoOp(t *testing.T) {
	h := session.NewHub()
	// No Register call for this campaign at all — must not panic or block.
	h.Broadcast("nobody-here", []byte("hello"))
}

func TestHub_Unregister_ClosesOutboxAndStopsDelivery(t *testing.T) {
	h := session.NewHub()
	c := h.Register("campaign-1", "player-a")

	h.Unregister(c)

	if _, ok := recvOrTimeout(t, c.Outbox(), time.Second); ok {
		t.Fatal("Outbox() yielded a value after Unregister, want it closed")
	}

	// A subsequent Broadcast to the now-empty campaign must not panic
	// (the client is gone, not just its channel closed).
	h.Broadcast("campaign-1", []byte("hello"))
}

func TestHub_Unregister_DoesNotAffectOtherClientsInSameCampaign(t *testing.T) {
	h := session.NewHub()
	leaving := h.Register("campaign-1", "player-a")
	staying := h.Register("campaign-1", "player-b")

	h.Unregister(leaving)
	h.Broadcast("campaign-1", []byte("hello"))

	if _, ok := recvOrTimeout(t, staying.Outbox(), time.Second); !ok {
		t.Fatal("staying client: no message received after the other client unregistered")
	}
}

func TestHub_Broadcast_FullOutbox_DropsRatherThanBlocks(t *testing.T) {
	h := session.NewHub()
	c := h.Register("campaign-1", "player-a")

	// Fill the mailbox to capacity without draining it.
	for i := 0; i < 16; i++ {
		h.Broadcast("campaign-1", []byte("filler"))
	}

	done := make(chan struct{})
	go func() {
		h.Broadcast("campaign-1", []byte("overflow"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on a full outbox instead of dropping the message")
	}

	if got := len(c.Outbox()); got != 16 {
		t.Errorf("len(Outbox()) = %d, want 16 (overflow message should have been dropped)", got)
	}
}

func TestHub_SendToSender_DeliversOnlyToMatchingSender(t *testing.T) {
	h := session.NewHub()
	a := h.Register("campaign-1", "player-a")
	b := h.Register("campaign-1", "player-b")

	h.SendToSender("campaign-1", "player-a", []byte("for-a-only"))

	got, ok := recvOrTimeout(t, a.Outbox(), time.Second)
	if !ok {
		t.Fatal("player-a: no message received")
	}
	if string(got) != "for-a-only" {
		t.Errorf("player-a: got %q, want %q", got, "for-a-only")
	}
	if got, ok := recvOrTimeout(t, b.Outbox(), 200*time.Millisecond); ok {
		t.Errorf("player-b: received %q, want nothing (SendToSender must not cross senders)", got)
	}
}

func TestHub_SendToSender_MultipleConnectionsSameSender_DeliversToAll(t *testing.T) {
	h := session.NewHub()
	tab1 := h.Register("campaign-1", "player-a")
	tab2 := h.Register("campaign-1", "player-a")

	h.SendToSender("campaign-1", "player-a", []byte("hello"))

	for name, c := range map[string]*session.Client{"tab1": tab1, "tab2": tab2} {
		if _, ok := recvOrTimeout(t, c.Outbox(), time.Second); !ok {
			t.Errorf("%s: no message received, want delivery to every connection for the same sender", name)
		}
	}
}

func TestHub_SendToSender_UnknownSender_NoOp(t *testing.T) {
	h := session.NewHub()
	h.Register("campaign-1", "player-a")

	// Must not panic or block.
	h.SendToSender("campaign-1", "nobody-registered", []byte("hello"))
}
