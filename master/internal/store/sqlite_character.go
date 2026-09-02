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

// SaveCharacter implements CharacterStore.
func (s *SQLiteEventStore) SaveCharacter(ctx context.Context, character Character) error {
	if character.CampaignID == "" {
		return ErrCampaignIDRequired
	}
	if character.ID == "" {
		return ErrCharacterIDRequired
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO characters (character_id, campaign_id, owner_id, schema_version, status, character_data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (character_id) DO UPDATE SET
			campaign_id = excluded.campaign_id,
			owner_id = excluded.owner_id,
			schema_version = excluded.schema_version,
			status = excluded.status,
			character_data = excluded.character_data,
			updated_at = excluded.updated_at`,
		character.ID, character.CampaignID, character.OwnerID, character.SchemaVersion,
		string(character.Status), string(character.CharacterData),
		character.CreatedAt.UTC().Format(occurredAtLayout), character.UpdatedAt.UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving character: %w", err)
	}
	return nil
}

// GetCharacter implements CharacterStore.
func (s *SQLiteEventStore) GetCharacter(ctx context.Context, characterID string) (Character, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT character_id, campaign_id, owner_id, schema_version, status, character_data, created_at, updated_at
		 FROM characters
		 WHERE character_id = ?`,
		characterID,
	)

	var c Character
	var status, characterData, createdAt, updatedAt string
	err := row.Scan(&c.ID, &c.CampaignID, &c.OwnerID, &c.SchemaVersion, &status, &characterData, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, fmt.Errorf("%w: character_id=%q", ErrCharacterNotFound, characterID)
	}
	if err != nil {
		return Character{}, fmt.Errorf("store: getting character: %w", err)
	}

	c.Status = CharacterStatus(status)
	c.CharacterData = json.RawMessage(characterData)
	c.CreatedAt, err = time.Parse(occurredAtLayout, createdAt)
	if err != nil {
		return Character{}, fmt.Errorf("store: parsing created_at for character %q: %w", characterID, err)
	}
	c.UpdatedAt, err = time.Parse(occurredAtLayout, updatedAt)
	if err != nil {
		return Character{}, fmt.Errorf("store: parsing updated_at for character %q: %w", characterID, err)
	}
	return c, nil
}
