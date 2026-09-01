// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package session implements Master's connection registry — which
// clients are currently connected to which campaign, and routing a
// message to every client in a campaign. This is the "session
// orchestration" primitive design doc §3.1 assigns to Master: package
// session knows nothing about protocol message shapes or the WebSocket
// transport itself, only which connections exist and how to reach them —
// package server owns decoding/encoding and the actual network I/O.
package session

import "sync"

// outboxSize is how many pending broadcast messages a Client's mailbox
// buffers before Hub.Broadcast starts dropping messages to it rather
// than blocking every other client's delivery on one slow connection.
// Generous for today's low message volume (safety.flag only); revisit
// once narrative/roll/map traffic is flowing.
const outboxSize = 16

// Client is one registered connection's outbound mailbox. Hub only ever
// sends into it; the owner (package server) is responsible for draining
// Outbox() in a dedicated goroutine and writing each message to the
// actual network connection.
type Client struct {
	campaignID string
	outbox     chan []byte
}

// Outbox returns the channel Hub delivers broadcast messages to. It is
// closed when the client is unregistered — range over it rather than
// receiving in a loop with an explicit exit check.
func (c *Client) Outbox() <-chan []byte {
	return c.outbox
}

// Hub tracks connected clients per campaign for broadcast routing. The
// zero value is not usable; construct with NewHub. A Hub is safe for
// concurrent use.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]map[*Client]struct{}
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*Client]struct{})}
}

// Register creates and returns a new Client registered under campaignID.
// The caller must call Unregister exactly once when the connection ends.
func (h *Hub) Register(campaignID string) *Client {
	c := &Client{campaignID: campaignID, outbox: make(chan []byte, outboxSize)}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[campaignID] == nil {
		h.rooms[campaignID] = make(map[*Client]struct{})
	}
	h.rooms[campaignID][c] = struct{}{}

	return c
}

// Unregister removes c from its campaign and closes its outbox, which
// ends the owner's drain loop over Outbox(). Call it exactly once per
// Client (typically via defer right after Register) — a second call
// panics, closing an already-closed channel, the same way Go's own
// close() would for any other double-close bug.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if room, ok := h.rooms[c.campaignID]; ok {
		delete(room, c)
		if len(room) == 0 {
			delete(h.rooms, c.campaignID)
		}
	}
	close(c.outbox)
}

// Broadcast delivers payload to every Client currently registered under
// campaignID — including, if it's still registered, whichever client (if
// any) triggered the broadcast; the caller decides whether that's
// desired for a given message type. Broadcasting to a campaign with no
// registered clients is a no-op, not an error.
//
// A client whose outbox is already full is skipped rather than blocking
// delivery to every other client on one stalled connection — that
// client will find the message in its history the next time it fetches
// the event log (design doc §10's pageable read surface), not lose it
// outright, since persistence (not the live broadcast) is Master's
// durability guarantee.
func (h *Hub) Broadcast(campaignID string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.rooms[campaignID] {
		select {
		case c.outbox <- payload:
		default:
		}
	}
}
