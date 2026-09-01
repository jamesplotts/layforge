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
}

// SQLiteEventStore is the SQLite-backed EventStore — Master's
// zero-config default persistence (design doc §10).
type SQLiteEventStore struct {
	db *sql.DB
}

var _ EventStore = (*SQLiteEventStore)(nil)

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

// ListEvents implements EventStore.
func (s *SQLiteEventStore) ListEvents(ctx context.Context, campaignID string, opts ListEventsOptions) ([]Event, error) {
	if campaignID == "" {
		return nil, ErrCampaignIDRequired
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, campaign_id, message_id, message_type, sender_id, occurred_at, payload
		 FROM events
		 WHERE campaign_id = ? AND sequence > ?
		 ORDER BY sequence ASC
		 LIMIT ?`,
		campaignID, opts.AfterSequence, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing events: %w", err)
	}
	defer rows.Close()

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
