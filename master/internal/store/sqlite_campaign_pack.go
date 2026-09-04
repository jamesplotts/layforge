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

var _ CampaignPackStore = (*SQLiteEventStore)(nil)

// SaveCampaignPack implements CampaignPackStore.
func (s *SQLiteEventStore) SaveCampaignPack(ctx context.Context, campaignID, packDir, packID string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO campaign_pack (campaign_id, pack_dir, pack_id, loaded_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET
			pack_dir = excluded.pack_dir,
			pack_id = excluded.pack_id,
			loaded_at = excluded.loaded_at`,
		campaignID, packDir, packID, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving campaign pack binding: %w", err)
	}
	return nil
}

// GetCampaignPack implements CampaignPackStore.
func (s *SQLiteEventStore) GetCampaignPack(ctx context.Context, campaignID string) (CampaignPack, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT pack_dir, pack_id, loaded_at FROM campaign_pack WHERE campaign_id = ?`, campaignID)

	var pack CampaignPack
	pack.CampaignID = campaignID
	var loadedAt string
	err := row.Scan(&pack.PackDir, &pack.PackID, &loadedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignPack{}, false, nil
	}
	if err != nil {
		return CampaignPack{}, false, fmt.Errorf("store: getting campaign pack binding: %w", err)
	}
	pack.LoadedAt, err = time.Parse(occurredAtLayout, loadedAt)
	if err != nil {
		return CampaignPack{}, false, fmt.Errorf("store: parsing campaign pack loaded_at: %w", err)
	}
	return pack, true, nil
}

// GetPartyLocation implements CampaignPackStore.
func (s *SQLiteEventStore) GetPartyLocation(ctx context.Context, campaignID string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT location_id FROM party_location WHERE campaign_id = ?`, campaignID)

	var locationID string
	err := row.Scan(&locationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: getting party location: %w", err)
	}
	return locationID, nil
}

// SetPartyLocation implements CampaignPackStore.
func (s *SQLiteEventStore) SetPartyLocation(ctx context.Context, campaignID, locationID string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO party_location (campaign_id, location_id)
		 VALUES (?, ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET location_id = excluded.location_id`,
		campaignID, locationID,
	)
	if err != nil {
		return fmt.Errorf("store: setting party location: %w", err)
	}
	return nil
}

// GetLocationState implements CampaignPackStore.
func (s *SQLiteEventStore) GetLocationState(ctx context.Context, campaignID, locationID string) (LocationState, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT discovered, claimed_by_party, claim_note FROM location_state WHERE campaign_id = ? AND location_id = ?`,
		campaignID, locationID,
	)

	state := LocationState{CampaignID: campaignID, LocationID: locationID}
	var discovered, claimed int
	err := row.Scan(&discovered, &claimed, &state.ClaimNote)
	if errors.Is(err, sql.ErrNoRows) {
		return LocationState{}, false, nil
	}
	if err != nil {
		return LocationState{}, false, fmt.Errorf("store: getting location state: %w", err)
	}
	state.Discovered = discovered != 0
	state.ClaimedByParty = claimed != 0
	return state, true, nil
}

// SetLocationDiscovered implements CampaignPackStore.
func (s *SQLiteEventStore) SetLocationDiscovered(ctx context.Context, campaignID, locationID string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO location_state (campaign_id, location_id, discovered)
		 VALUES (?, ?, 1)
		 ON CONFLICT (campaign_id, location_id) DO UPDATE SET discovered = 1`,
		campaignID, locationID,
	)
	if err != nil {
		return fmt.Errorf("store: setting location discovered: %w", err)
	}
	return nil
}

// SetLocationClaimed implements CampaignPackStore.
func (s *SQLiteEventStore) SetLocationClaimed(ctx context.Context, campaignID, locationID, note string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO location_state (campaign_id, location_id, claimed_by_party, claim_note)
		 VALUES (?, ?, 1, ?)
		 ON CONFLICT (campaign_id, location_id) DO UPDATE SET
			claimed_by_party = 1,
			claim_note = excluded.claim_note`,
		campaignID, locationID, note,
	)
	if err != nil {
		return fmt.Errorf("store: setting location claimed: %w", err)
	}
	return nil
}

// StashItem implements CampaignPackStore.
func (s *SQLiteEventStore) StashItem(ctx context.Context, id, campaignID, locationID, characterID, itemName string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stashed_items (id, campaign_id, location_id, character_id, item_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, campaignID, locationID, characterID, itemName, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: stashing item: %w", err)
	}
	return nil
}

// RetrieveItem implements CampaignPackStore.
func (s *SQLiteEventStore) RetrieveItem(ctx context.Context, campaignID, locationID, characterID, itemName string) (bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id FROM stashed_items
		 WHERE campaign_id = ? AND location_id = ? AND character_id = ? AND item_name = ?
		 LIMIT 1`,
		campaignID, locationID, characterID, itemName,
	)
	var id string
	err := row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: finding stashed item: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM stashed_items WHERE id = ?`, id); err != nil {
		return false, fmt.Errorf("store: retrieving stashed item: %w", err)
	}
	return true, nil
}

// ListStashedItems implements CampaignPackStore.
func (s *SQLiteEventStore) ListStashedItems(ctx context.Context, campaignID, locationID string) ([]StashedItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, character_id, item_name, created_at FROM stashed_items
		 WHERE campaign_id = ? AND location_id = ?
		 ORDER BY created_at`,
		campaignID, locationID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing stashed items: %w", err)
	}
	defer rows.Close()

	var items []StashedItem
	for rows.Next() {
		item := StashedItem{CampaignID: campaignID, LocationID: locationID}
		var createdAt string
		if err := rows.Scan(&item.ID, &item.CharacterID, &item.ItemName, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scanning stashed item row: %w", err)
		}
		item.CreatedAt, err = time.Parse(occurredAtLayout, createdAt)
		if err != nil {
			return nil, fmt.Errorf("store: parsing stashed item created_at: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading stashed item rows: %w", err)
	}
	return items, nil
}

// AddStashedCurrency implements CampaignPackStore.
func (s *SQLiteEventStore) AddStashedCurrency(ctx context.Context, campaignID, locationID, characterID string, copper, silver, gold, platinum int32) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stashed_currency (campaign_id, location_id, character_id, copper, silver, gold, platinum)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (campaign_id, location_id, character_id) DO UPDATE SET
			copper = copper + excluded.copper,
			silver = silver + excluded.silver,
			gold = gold + excluded.gold,
			platinum = platinum + excluded.platinum`,
		campaignID, locationID, characterID, copper, silver, gold, platinum,
	)
	if err != nil {
		return fmt.Errorf("store: depositing stashed currency: %w", err)
	}
	return nil
}

// RemoveStashedCurrency implements CampaignPackStore.
func (s *SQLiteEventStore) RemoveStashedCurrency(ctx context.Context, campaignID, locationID, characterID string, copper, silver, gold, platinum int32) error {
	haveCopper, haveSilver, haveGold, havePlatinum, err := s.GetStashedCurrency(ctx, campaignID, locationID, characterID)
	if err != nil {
		return err
	}
	if copper > haveCopper || silver > haveSilver || gold > haveGold || platinum > havePlatinum {
		return ErrInsufficientStashedCurrency
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE stashed_currency SET
			copper = copper - ?,
			silver = silver - ?,
			gold = gold - ?,
			platinum = platinum - ?
		 WHERE campaign_id = ? AND location_id = ? AND character_id = ?`,
		copper, silver, gold, platinum, campaignID, locationID, characterID,
	)
	if err != nil {
		return fmt.Errorf("store: withdrawing stashed currency: %w", err)
	}
	return nil
}

// GetStashedCurrency implements CampaignPackStore.
func (s *SQLiteEventStore) GetStashedCurrency(ctx context.Context, campaignID, locationID, characterID string) (copper, silver, gold, platinum int32, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT copper, silver, gold, platinum FROM stashed_currency
		 WHERE campaign_id = ? AND location_id = ? AND character_id = ?`,
		campaignID, locationID, characterID,
	)
	err = row.Scan(&copper, &silver, &gold, &platinum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("store: getting stashed currency: %w", err)
	}
	return copper, silver, gold, platinum, nil
}
