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

var _ AdminSettingsStore = (*SQLiteEventStore)(nil)

// GetCampaignSettings implements AdminSettingsStore.
func (s *SQLiteEventStore) GetCampaignSettings(ctx context.Context, campaignID string) (CampaignSettings, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT pvp_policy, pvp_consent, maturity_tier_prompt, image_maturity_tier_prompt, room_password
		 FROM campaign_settings
		 WHERE campaign_id = ?`,
		campaignID,
	)

	var settings CampaignSettings
	var pvpConsent string
	err := row.Scan(&settings.PvPPolicy, &pvpConsent, &settings.MaturityTierPrompt, &settings.ImageMaturityTierPrompt, &settings.RoomPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignSettings{}, false, nil
	}
	if err != nil {
		return CampaignSettings{}, false, fmt.Errorf("store: getting campaign settings: %w", err)
	}
	if err := json.Unmarshal([]byte(pvpConsent), &settings.PvPConsent); err != nil {
		return CampaignSettings{}, false, fmt.Errorf("store: parsing pvp_consent for campaign %q: %w", campaignID, err)
	}
	return settings, true, nil
}

// SaveCampaignSettings implements AdminSettingsStore.
func (s *SQLiteEventStore) SaveCampaignSettings(ctx context.Context, campaignID string, settings CampaignSettings) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}

	pvpConsent, err := json.Marshal(settings.PvPConsent)
	if err != nil {
		return fmt.Errorf("store: marshaling pvp_consent for campaign %q: %w", campaignID, err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO campaign_settings (campaign_id, pvp_policy, pvp_consent, maturity_tier_prompt, image_maturity_tier_prompt, room_password, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET
			pvp_policy = excluded.pvp_policy,
			pvp_consent = excluded.pvp_consent,
			maturity_tier_prompt = excluded.maturity_tier_prompt,
			image_maturity_tier_prompt = excluded.image_maturity_tier_prompt,
			room_password = excluded.room_password,
			updated_at = excluded.updated_at`,
		campaignID, settings.PvPPolicy, string(pvpConsent), settings.MaturityTierPrompt, settings.ImageMaturityTierPrompt, settings.RoomPassword,
		time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving campaign settings: %w", err)
	}
	return nil
}

// ListCampaignIDs implements AdminSettingsStore.
func (s *SQLiteEventStore) ListCampaignIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT campaign_id FROM events
		 UNION SELECT campaign_id FROM characters
		 UNION SELECT campaign_id FROM campaign_settings
		 ORDER BY campaign_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing campaign ids: %w", err)
	}
	defer rows.Close()

	var campaignIDs []string
	for rows.Next() {
		var campaignID string
		if err := rows.Scan(&campaignID); err != nil {
			return nil, fmt.Errorf("store: scanning campaign id row: %w", err)
		}
		campaignIDs = append(campaignIDs, campaignID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading campaign id rows: %w", err)
	}
	return campaignIDs, nil
}

// ListCampaignSummaries implements AdminSettingsStore. last_active_at
// prefers the most recent event timestamp, falling back to the most
// recent character update for a campaign with characters but no events
// yet (freshly uploaded, no play started) — either LEFT JOIN misses
// leave it NULL, which COALESCE resolves to '' (parsed as the zero
// time.Time below).
func (s *SQLiteEventStore) ListCampaignSummaries(ctx context.Context) ([]CampaignSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
			ids.campaign_id,
			COALESCE(cm.display_name, '') AS display_name,
			COALESCE(pc.party_count, 0) AS party_count,
			COALESCE(ev.last_active_at, ch.last_active_at, '') AS last_active_at,
			COALESCE(cm.archived, 0) AS archived
		 FROM (
			SELECT campaign_id FROM events
			UNION SELECT campaign_id FROM characters
			UNION SELECT campaign_id FROM campaign_settings
			UNION SELECT campaign_id FROM campaign_meta
		 ) ids
		 LEFT JOIN campaign_meta cm ON cm.campaign_id = ids.campaign_id
		 LEFT JOIN (SELECT campaign_id, COUNT(*) AS party_count FROM characters GROUP BY campaign_id) pc
			ON pc.campaign_id = ids.campaign_id
		 LEFT JOIN (SELECT campaign_id, MAX(occurred_at) AS last_active_at FROM events GROUP BY campaign_id) ev
			ON ev.campaign_id = ids.campaign_id
		 LEFT JOIN (SELECT campaign_id, MAX(updated_at) AS last_active_at FROM characters GROUP BY campaign_id) ch
			ON ch.campaign_id = ids.campaign_id
		 ORDER BY ids.campaign_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing campaign summaries: %w", err)
	}
	defer rows.Close()

	var summaries []CampaignSummary
	for rows.Next() {
		var summary CampaignSummary
		var lastActiveAt string
		var archived int
		if err := rows.Scan(&summary.CampaignID, &summary.DisplayName, &summary.PartyCount, &lastActiveAt, &archived); err != nil {
			return nil, fmt.Errorf("store: scanning campaign summary row: %w", err)
		}
		if lastActiveAt != "" {
			parsed, err := time.Parse(occurredAtLayout, lastActiveAt)
			if err != nil {
				return nil, fmt.Errorf("store: parsing last_active_at for campaign %q: %w", summary.CampaignID, err)
			}
			summary.LastActiveAt = parsed
		}
		summary.Archived = archived != 0
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading campaign summary rows: %w", err)
	}
	return summaries, nil
}

// SaveCampaignMeta implements AdminSettingsStore.
func (s *SQLiteEventStore) SaveCampaignMeta(ctx context.Context, campaignID, displayName string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO campaign_meta (campaign_id, display_name, archived, archived_at, created_at)
		 VALUES (?, ?, 0, '', ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET display_name = excluded.display_name`,
		campaignID, displayName, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: saving campaign meta: %w", err)
	}
	return nil
}

// SetCampaignArchived implements AdminSettingsStore.
func (s *SQLiteEventStore) SetCampaignArchived(ctx context.Context, campaignID string, archived bool) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}

	archivedInt := 0
	archivedAt := ""
	if archived {
		archivedInt = 1
		archivedAt = time.Now().UTC().Format(occurredAtLayout)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO campaign_meta (campaign_id, display_name, archived, archived_at, created_at)
		 VALUES (?, '', ?, ?, ?)
		 ON CONFLICT (campaign_id) DO UPDATE SET archived = excluded.archived, archived_at = excluded.archived_at`,
		campaignID, archivedInt, archivedAt, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: setting campaign archived: %w", err)
	}
	return nil
}

// GetSystemSettings implements AdminSettingsStore.
func (s *SQLiteEventStore) GetSystemSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, fmt.Errorf("store: getting system settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: scanning system setting row: %w", err)
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading system setting rows: %w", err)
	}
	return settings, nil
}

// SaveSystemSettings implements AdminSettingsStore. Each key/value pair
// is upserted independently within a single transaction — either all of
// settings is saved or none of it is, so a mid-batch failure never leaves
// System-tab keys in a half-updated state.
func (s *SQLiteEventStore) SaveSystemSettings(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: saving system settings: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(occurredAtLayout)
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO system_settings (key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
	)
	if err != nil {
		return fmt.Errorf("store: saving system settings: %w", err)
	}
	defer stmt.Close()

	for key, value := range settings {
		if _, err := stmt.ExecContext(ctx, key, value, now); err != nil {
			return fmt.Errorf("store: saving system setting %q: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: saving system settings: %w", err)
	}
	return nil
}
