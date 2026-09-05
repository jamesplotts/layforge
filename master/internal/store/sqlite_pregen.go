// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SavePregen implements PregenStore.
func (s *SQLiteEventStore) SavePregen(ctx context.Context, pregen Pregen) error {
	if pregen.CampaignID == "" {
		return ErrCampaignIDRequired
	}
	if pregen.ID == "" {
		return ErrPregenIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pregens (id, campaign_id, name, description, schema_version, character_data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
			campaign_id = excluded.campaign_id,
			name = excluded.name,
			description = excluded.description,
			schema_version = excluded.schema_version,
			character_data = excluded.character_data`,
		pregen.ID, pregen.CampaignID, pregen.Name, pregen.Description,
		pregen.SchemaVersion, string(pregen.CharacterData),
		pregen.CreatedAt.UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving pregen: %w", err)
	}
	return nil
}

// GetPregen implements PregenStore.
func (s *SQLiteEventStore) GetPregen(ctx context.Context, pregenID string) (Pregen, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, campaign_id, name, description, schema_version, character_data, created_at
		 FROM pregens WHERE id = ?`,
		pregenID,
	)
	pregen, err := scanPregen(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Pregen{}, fmt.Errorf("%w: id=%q", ErrPregenNotFound, pregenID)
	}
	if err != nil {
		return Pregen{}, fmt.Errorf("store: getting pregen: %w", err)
	}
	return pregen, nil
}

// ListPregens implements PregenStore.
func (s *SQLiteEventStore) ListPregens(ctx context.Context, campaignID string) ([]Pregen, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, campaign_id, name, description, schema_version, character_data, created_at
		 FROM pregens WHERE campaign_id = ?`,
		campaignID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing pregens: %w", err)
	}
	defer rows.Close()

	pregens := make([]Pregen, 0)
	for rows.Next() {
		pregen, err := scanPregen(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: scanning pregen row: %w", err)
		}
		pregens = append(pregens, pregen)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating pregen rows: %w", err)
	}
	return pregens, nil
}

// DeletePregen implements PregenStore.
func (s *SQLiteEventStore) DeletePregen(ctx context.Context, pregenID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pregens WHERE id = ?`, pregenID)
	if err != nil {
		return fmt.Errorf("store: deleting pregen: %w", err)
	}
	return nil
}

// scanPregen scans one pregens row via scan (either *sql.Row.Scan or
// *sql.Rows.Scan — both share this signature), shared by GetPregen and
// ListPregens so the column order/parsing lives in exactly one place.
func scanPregen(scan func(...any) error) (Pregen, error) {
	var p Pregen
	var characterData, createdAt string
	if err := scan(&p.ID, &p.CampaignID, &p.Name, &p.Description, &p.SchemaVersion, &characterData, &createdAt); err != nil {
		return Pregen{}, err
	}
	p.CharacterData = json.RawMessage(characterData)
	var err error
	p.CreatedAt, err = time.Parse(occurredAtLayout, createdAt)
	if err != nil {
		return Pregen{}, fmt.Errorf("parsing created_at for pregen %q: %w", p.ID, err)
	}
	return p, nil
}
