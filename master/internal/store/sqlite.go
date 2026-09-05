// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlitedriver "modernc.org/sqlite"
)

// sqliteConstraintUnique is SQLite's extended result code
// SQLITE_CONSTRAINT_UNIQUE — confirmed against modernc.org/sqlite v1.45.0
// by triggering a real UNIQUE violation, since the driver doesn't export
// this as a named constant (see isUniqueConstraintErr).
const sqliteConstraintUnique = 2067

// occurredAtLayout is the on-disk timestamp format: RFC3339 with
// nanosecond precision, so ordering by the text column matches
// chronological order without a custom SQLite collation.
const occurredAtLayout = time.RFC3339Nano

// initStatements creates the schema if it doesn't already exist and sets
// the pragmas Master needs for a single process making concurrent
// WebSocket-connection-driven writes: WAL so readers don't block the
// writer, and a busy_timeout so a momentary lock contends instead of
// immediately failing with SQLITE_BUSY. Run individually (not as one
// multi-statement Exec) so this doesn't depend on the driver supporting
// multiple statements per call.
var initStatements = []string{
	`PRAGMA journal_mode = WAL;`,
	`PRAGMA busy_timeout = 5000;`,
	`CREATE TABLE IF NOT EXISTS events (
		sequence     INTEGER PRIMARY KEY AUTOINCREMENT,
		campaign_id  TEXT NOT NULL,
		message_id   TEXT NOT NULL,
		message_type TEXT NOT NULL,
		sender_id    TEXT NOT NULL,
		occurred_at  TEXT NOT NULL,
		payload      TEXT NOT NULL,
		UNIQUE (campaign_id, message_id)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_events_campaign_sequence ON events (campaign_id, sequence);`,
	`CREATE TABLE IF NOT EXISTS characters (
		character_id   TEXT PRIMARY KEY,
		campaign_id    TEXT NOT NULL,
		owner_id       TEXT NOT NULL,
		schema_version TEXT NOT NULL,
		status         TEXT NOT NULL,
		character_data TEXT NOT NULL,
		created_at     TEXT NOT NULL,
		updated_at     TEXT NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_characters_campaign ON characters (campaign_id);`,
	`CREATE TABLE IF NOT EXISTS campaign_settings (
		campaign_id                TEXT PRIMARY KEY,
		pvp_policy                 TEXT NOT NULL DEFAULT '',
		pvp_consent                TEXT NOT NULL DEFAULT '[]',
		maturity_tier_prompt       TEXT NOT NULL DEFAULT '',
		image_maturity_tier_prompt TEXT NOT NULL DEFAULT '',
		room_password              TEXT NOT NULL DEFAULT '',
		price_multiplier           REAL NOT NULL DEFAULT 0,
		updated_at                 TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS system_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS combat_state (
		campaign_id TEXT PRIMARY KEY,
		payload     TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS campaign_meta (
		campaign_id  TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		archived     INTEGER NOT NULL DEFAULT 0,
		archived_at  TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS campaign_pack (
		campaign_id TEXT PRIMARY KEY,
		pack_dir    TEXT NOT NULL,
		pack_id     TEXT NOT NULL,
		loaded_at   TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS party_location (
		campaign_id TEXT PRIMARY KEY,
		location_id TEXT NOT NULL DEFAULT ''
	);`,
	`CREATE TABLE IF NOT EXISTS location_state (
		campaign_id      TEXT NOT NULL,
		location_id      TEXT NOT NULL,
		discovered       INTEGER NOT NULL DEFAULT 0,
		claimed_by_party INTEGER NOT NULL DEFAULT 0,
		claim_note       TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (campaign_id, location_id)
	);`,
	`CREATE TABLE IF NOT EXISTS stashed_items (
		id           TEXT PRIMARY KEY,
		campaign_id  TEXT NOT NULL,
		location_id  TEXT NOT NULL,
		character_id TEXT NOT NULL,
		item_name    TEXT NOT NULL,
		created_at   TEXT NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_stashed_items_lookup ON stashed_items (campaign_id, location_id, character_id);`,
	`CREATE TABLE IF NOT EXISTS stashed_currency (
		campaign_id  TEXT NOT NULL,
		location_id  TEXT NOT NULL,
		character_id TEXT NOT NULL,
		copper       INTEGER NOT NULL DEFAULT 0,
		silver       INTEGER NOT NULL DEFAULT 0,
		gold         INTEGER NOT NULL DEFAULT 0,
		platinum     INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (campaign_id, location_id, character_id)
	);`,
	`CREATE TABLE IF NOT EXISTS vehicles (
		id           TEXT PRIMARY KEY,
		campaign_id  TEXT NOT NULL,
		name         TEXT NOT NULL,
		vehicle_type TEXT NOT NULL,
		stabled      INTEGER NOT NULL DEFAULT 0,
		location_id  TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_vehicles_campaign ON vehicles (campaign_id);`,
	`CREATE TABLE IF NOT EXISTS pregens (
		id             TEXT PRIMARY KEY,
		campaign_id    TEXT NOT NULL,
		name           TEXT NOT NULL,
		description    TEXT NOT NULL DEFAULT '',
		schema_version TEXT NOT NULL,
		character_data TEXT NOT NULL,
		created_at     TEXT NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_pregens_campaign ON pregens (campaign_id);`,
}

// SQLiteEventStore is the SQLite-backed EventStore — Master's
// zero-config default persistence (design doc §10). It also implements
// CharacterStore (see sqlite_character.go): both share the same
// underlying database/connection, the same way a single "Store" backed by
// one *sql.DB commonly satisfies several narrow repository interfaces in
// Go, rather than each interface needing its own connection or type name.
type SQLiteEventStore struct {
	db *sql.DB
}

var _ EventStore = (*SQLiteEventStore)(nil)
var _ CharacterStore = (*SQLiteEventStore)(nil)

// OpenSQLiteEventStore opens (creating if necessary) a SQLite database at
// dsn and ensures its schema exists. dsn is passed directly to the
// modernc.org/sqlite driver — a file path, or ":memory:" for an
// in-process database useful in tests.
func OpenSQLiteEventStore(dsn string) (*SQLiteEventStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening sqlite database: %w", err)
	}

	if isInMemoryDSN(dsn) {
		// An in-memory SQLite database only exists for the lifetime of
		// the connection that created it; without this, database/sql's
		// connection pool would silently hand a second query a fresh,
		// empty database on a different connection.
		db.SetMaxOpenConns(1)
	}

	for _, stmt := range initStatements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: initializing schema: %w", err)
		}
	}

	return &SQLiteEventStore{db: db}, nil
}

func isInMemoryDSN(dsn string) bool {
	return dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
}

// Close releases the underlying database connection(s).
func (s *SQLiteEventStore) Close() error {
	return s.db.Close()
}

// AppendEvent implements EventStore.
func (s *SQLiteEventStore) AppendEvent(ctx context.Context, event Event) error {
	if event.CampaignID == "" {
		return ErrCampaignIDRequired
	}
	if event.MessageID == "" {
		return ErrMessageIDRequired
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (campaign_id, message_id, message_type, sender_id, occurred_at, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.CampaignID, event.MessageID, event.MessageType, event.SenderID,
		event.OccurredAt.UTC().Format(occurredAtLayout), string(event.Raw),
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return fmt.Errorf("%w: campaign_id=%q message_id=%q", ErrDuplicateMessage, event.CampaignID, event.MessageID)
		}
		return fmt.Errorf("store: appending event: %w", err)
	}
	return nil
}

// ListEvents implements EventStore. See that method's doc comment for
// the direction/default semantics; this just dispatches to the forward
// or backward query shape and leaves the SQL to listForward/listBackward.
func (s *SQLiteEventStore) ListEvents(ctx context.Context, campaignID string, opts ListEventsOptions) ([]Event, bool, error) {
	if campaignID == "" {
		return nil, false, ErrCampaignIDRequired
	}
	if opts.AfterSequence != 0 && opts.BeforeSequence != 0 {
		return nil, false, ErrConflictingPagination
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	if opts.AfterSequence != 0 {
		return s.listForward(ctx, campaignID, opts.AfterSequence, limit)
	}
	return s.listBackward(ctx, campaignID, opts.BeforeSequence, limit)
}

// listForward returns events with Sequence > afterSequence, oldest
// first, plus whether more (newer) events exist beyond the page.
func (s *SQLiteEventStore) listForward(ctx context.Context, campaignID string, afterSequence int64, limit int) ([]Event, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, campaign_id, message_id, message_type, sender_id, occurred_at, payload
		 FROM events
		 WHERE campaign_id = ? AND sequence > ?
		 ORDER BY sequence ASC
		 LIMIT ?`,
		campaignID, afterSequence, limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("store: listing events: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return events, hasMore, nil
}

// listBackward returns the Limit events with Sequence < beforeSequence
// nearest to it (or, if beforeSequence is zero, the most recent Limit
// events in the campaign — the tail/default case), oldest first, plus
// whether more (older) events exist beyond the page.
func (s *SQLiteEventStore) listBackward(ctx context.Context, campaignID string, beforeSequence int64, limit int) ([]Event, bool, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if beforeSequence != 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT sequence, campaign_id, message_id, message_type, sender_id, occurred_at, payload
			 FROM events
			 WHERE campaign_id = ? AND sequence < ?
			 ORDER BY sequence DESC
			 LIMIT ?`,
			campaignID, beforeSequence, limit+1,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT sequence, campaign_id, message_id, message_type, sender_id, occurred_at, payload
			 FROM events
			 WHERE campaign_id = ?
			 ORDER BY sequence DESC
			 LIMIT ?`,
			campaignID, limit+1,
		)
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: listing events: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, false, err
	}
	// Rows arrive newest-first (closest to beforeSequence first), so the
	// (limit+1)th row, if present, is the farthest/oldest one — trim it
	// with events[:limit] while still in this descending order (that's
	// "drop the last element"), then reverse to the ascending order
	// ListEvents always returns.
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	reverseEvents(events)
	return events, hasMore, nil
}

// scanEvents drains rows into Events, shared by listForward and
// listBackward regardless of the SQL ORDER BY direction each used —
// callers reverse the result themselves if they queried descending.
func scanEvents(rows *sql.Rows) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var e Event
		var occurredAt, payload string
		if err := rows.Scan(&e.Sequence, &e.CampaignID, &e.MessageID, &e.MessageType, &e.SenderID, &occurredAt, &payload); err != nil {
			return nil, fmt.Errorf("store: scanning event row: %w", err)
		}
		parsed, err := time.Parse(occurredAtLayout, occurredAt)
		if err != nil {
			return nil, fmt.Errorf("store: parsing occurred_at for event sequence %d: %w", e.Sequence, err)
		}
		e.OccurredAt = parsed
		e.Raw = json.RawMessage(payload)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading event rows: %w", err)
	}
	return events, nil
}

// reverseEvents reverses events in place — used to turn a descending
// (newest-first) query result back into the ascending (oldest-first)
// order ListEvents always returns, regardless of paging direction.
func reverseEvents(events []Event) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE
// constraint violation, so AppendEvent can translate it into
// ErrDuplicateMessage rather than a generic store error.
func isUniqueConstraintErr(err error) bool {
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqliteConstraintUnique
	}
	return false
}
