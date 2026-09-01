// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package store implements Master's persistence layer: a repository/DAO
// abstraction over storage rather than direct file I/O, per design doc
// §10. SQLite (see SQLiteEventStore) is the zero-config default; a
// future Postgres implementation for larger/concurrent deployments would
// satisfy the same EventStore interface.
//
// This package deliberately does not import package protocol. It
// persists whatever raw JSON a caller hands it and returns that JSON
// unchanged — the caller (which does know the current set of protocol
// message shapes) is responsible for marshaling a protocol.Message[T]
// into an Event and unmarshaling it back. That keeps this package
// decoupled from a set of message types that will keep growing.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// DefaultListLimit is how many events ListEvents returns when
// ListEventsOptions.Limit is zero or negative.
const DefaultListLimit = 100

// MaxListLimit is the most events a single ListEvents call will ever
// return, regardless of a larger requested Limit — a caller building a
// pageable UI (design doc §10) is expected to page rather than request
// an unbounded result set.
const MaxListLimit = 1000

// Event is one durable entry in a campaign's append-only event log —
// storage's view of a protocol message, not the message itself.
type Event struct {
	// Sequence is the store-assigned, strictly increasing position of
	// this event within its campaign's log. Zero when passed to
	// AppendEvent (the store assigns it); always non-zero when returned
	// by ListEvents. Use this, not OccurredAt, as a pagination cursor —
	// OccurredAt is client-supplied and isn't guaranteed monotonic across
	// senders with clock skew (design doc §5 discusses message_id/
	// timestamp for exactly this reason).
	Sequence int64

	CampaignID  string
	MessageID   string
	MessageType string
	SenderID    string
	OccurredAt  time.Time

	// Raw is the full serialized protocol message (envelope + payload)
	// this event records.
	Raw json.RawMessage
}

// ListEventsOptions controls pagination for ListEvents.
type ListEventsOptions struct {
	// AfterSequence returns only events with Sequence > AfterSequence.
	// Zero means "from the beginning of the campaign's log".
	AfterSequence int64
	// Limit caps the number of events returned. Zero or negative uses
	// DefaultListLimit; anything above MaxListLimit is capped to it.
	Limit int
}

// Errors returned by EventStore implementations. Callers that need to
// distinguish failure reasons should use errors.Is against these rather
// than comparing error strings.
var (
	ErrCampaignIDRequired = errors.New("store: campaign_id is required")
	ErrMessageIDRequired  = errors.New("store: message_id is required")
	ErrDuplicateMessage   = errors.New("store: an event with this message_id already exists for this campaign")
)

// EventStore is Master's durable, append-only campaign event log — the
// backing store for chat/history review, tool-call audit logging, and
// spotlight-balance tracking (design doc §8, §9.6, §10), all of which
// read the same underlying stream rather than each maintaining their own.
type EventStore interface {
	// AppendEvent durably records event. It fails with
	// ErrCampaignIDRequired or ErrMessageIDRequired if either field is
	// empty, and with ErrDuplicateMessage if an event with the same
	// (CampaignID, MessageID) has already been recorded — message_id is
	// defined to be unique per design doc §5's ack/dedup requirement, so
	// a duplicate here indicates a retry or a bug, not a new event.
	AppendEvent(ctx context.Context, event Event) error

	// ListEvents returns events for campaignID in Sequence order,
	// starting after opts.AfterSequence, oldest first. It fails with
	// ErrCampaignIDRequired if campaignID is empty.
	ListEvents(ctx context.Context, campaignID string, opts ListEventsOptions) ([]Event, error)
}
