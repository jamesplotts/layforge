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

import (
	"sort"
	"sync"
)

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
	senderID   string
	outbox     chan []byte
	// close is invoked by Hub.Kick to forcibly end this client's
	// underlying connection — nil until SetCloser is called (every real
	// production connection sets one immediately after Register; a test
	// Client that never calls SetCloser is simply skipped by Kick).
	close func()
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

// Register creates and returns a new Client registered under campaignID,
// attributed to senderID (the sender_id from that connection's
// system.connect handshake) — needed so SendToSender can later target this
// specific player's connection(s), as opposed to Broadcast's room-wide
// delivery. The caller must call Unregister exactly once when the
// connection ends.
func (h *Hub) Register(campaignID, senderID string) *Client {
	c := &Client{campaignID: campaignID, senderID: senderID, outbox: make(chan []byte, outboxSize)}

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

// SendToSender delivers payload only to Client(s) currently registered
// under campaignID whose senderID matches sender — unlike Broadcast, other
// players in the same campaign never see it. A sender may have more than
// one connection open (multiple tabs); all of them receive it. Used for
// per-player fog-of-war map.token_state sends (internal/server/
// combat_map.go), where two connected players can legitimately be sent
// different payloads for the same underlying event — something Broadcast
// cannot express at all. Sending to a sender with no registered
// connections is a no-op, not an error; same drop-if-full semantics as
// Broadcast, for the same reason.
func (h *Hub) SendToSender(campaignID, sender string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.rooms[campaignID] {
		if c.senderID != sender {
			continue
		}
		select {
		case c.outbox <- payload:
		default:
		}
	}
}

// SetCloser attaches the function Hub.Kick invokes to forcibly end c's
// underlying connection. Package session has no transport dependency
// (see this file's own package doc comment) — the closer is an opaque
// callback the owner (package server) supplies, typically closing that
// connection's own real network connection. Safe to call at any point
// after Register.
func (h *Hub) SetCloser(c *Client, close func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.close = close
}

// Kick signals every currently-registered connection for sender in
// campaignID to close, via whichever closer each was given (a
// connection with no closer set — SetCloser never called — is skipped,
// not panicked on). Returns true if at least one connection was
// signaled. Kicking is not itself Unregister: the owning connection's
// own read loop notices the close, returns, and its existing deferred
// Unregister runs exactly as it does for any other disconnect — Kick
// never touches h.rooms directly.
//
// Closers are collected under h.mu but invoked after releasing it — a
// real close can perform its own close handshake and block for a
// timeout; holding h.mu for that long would stall Broadcast/
// SendToSender/Register for every other campaign on the whole process,
// the same class of problem Broadcast's own drop-if-full design already
// avoids for slow deliveries.
func (h *Hub) Kick(campaignID, senderID string) bool {
	h.mu.Lock()
	var closers []func()
	for c := range h.rooms[campaignID] {
		if c.senderID == senderID && c.close != nil {
			closers = append(closers, c.close)
		}
	}
	h.mu.Unlock()

	for _, close := range closers {
		close()
	}
	return len(closers) > 0
}

// ConnectedSenders returns the distinct sender_ids with at least one
// live connection registered under campaignID, sorted for a stable
// admin-UI listing (a sender with multiple open connections/tabs
// appears once). Returns nil, not an error, for a campaign with no
// registered clients.
func (h *Hub) ConnectedSenders(campaignID string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	seen := make(map[string]struct{})
	for c := range h.rooms[campaignID] {
		seen[c.senderID] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	senders := make([]string, 0, len(seen))
	for s := range seen {
		senders = append(senders, s)
	}
	sort.Strings(senders)
	return senders
}
