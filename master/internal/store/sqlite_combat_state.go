// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var _ CombatStateStore = (*SQLiteEventStore)(nil)

// SaveCombatState implements CombatStateStore.
func (s *SQLiteEventStore) SaveCombatState(ctx context.Context, campaignID string, payload []byte) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO combat_state (campaign_id, payload, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET
			payload = excluded.payload,
			updated_at = excluded.updated_at`,
		campaignID, string(payload), time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving combat state: %w", err)
	}
	return nil
}

// LoadCombatState implements CombatStateStore.
func (s *SQLiteEventStore) LoadCombatState(ctx context.Context, campaignID string) ([]byte, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT payload FROM combat_state WHERE campaign_id = ?`, campaignID)

	var payload string
	err := row.Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: loading combat state: %w", err)
	}
	return []byte(payload), true, nil
}

// DeleteCombatState implements CombatStateStore.
func (s *SQLiteEventStore) DeleteCombatState(ctx context.Context, campaignID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM combat_state WHERE campaign_id = ?`, campaignID)
	if err != nil {
		return fmt.Errorf("store: deleting combat state: %w", err)
	}
	return nil
}

// ListCombatStateCampaignIDs implements CombatStateStore.
func (s *SQLiteEventStore) ListCombatStateCampaignIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT campaign_id FROM combat_state ORDER BY campaign_id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing combat state campaign ids: %w", err)
	}
	defer rows.Close()

	var campaignIDs []string
	for rows.Next() {
		var campaignID string
		if err := rows.Scan(&campaignID); err != nil {
			return nil, fmt.Errorf("store: scanning combat state campaign id row: %w", err)
		}
		campaignIDs = append(campaignIDs, campaignID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading combat state campaign id rows: %w", err)
	}
	return campaignIDs, nil
}
