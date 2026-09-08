// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"fmt"
	"time"
)

var _ SafetyStore = (*SQLiteEventStore)(nil)

// AddSafetyFlag implements SafetyStore.
func (s *SQLiteEventStore) AddSafetyFlag(ctx context.Context, campaignID, topic string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}

	// ON CONFLICT DO NOTHING keeps the original flagged_at (and the
	// insertion order it implies) when the same topic is flagged again —
	// re-flagging is a no-op, per the interface contract.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO safety_flags (campaign_id, topic, flagged_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (campaign_id, topic) DO NOTHING`,
		campaignID, topic, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: adding safety flag: %w", err)
	}
	return nil
}

// ListSafetyFlags implements SafetyStore.
func (s *SQLiteEventStore) ListSafetyFlags(ctx context.Context, campaignID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT topic FROM safety_flags WHERE campaign_id = ? ORDER BY flagged_at, topic`,
		campaignID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing safety flags: %w", err)
	}
	defer rows.Close()

	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			return nil, fmt.Errorf("store: scanning safety flag row: %w", err)
		}
		topics = append(topics, topic)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading safety flag rows: %w", err)
	}
	return topics, nil
}
