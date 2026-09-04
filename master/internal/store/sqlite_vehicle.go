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

var _ VehicleStore = (*SQLiteEventStore)(nil)

// CreateVehicle implements VehicleStore.
func (s *SQLiteEventStore) CreateVehicle(ctx context.Context, id, campaignID, name, vehicleType string) error {
	if campaignID == "" {
		return ErrCampaignIDRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vehicles (id, campaign_id, name, vehicle_type, stabled, location_id, created_at)
		 VALUES (?, ?, ?, ?, 0, '', ?)`,
		id, campaignID, name, vehicleType, time.Now().UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: creating vehicle: %w", err)
	}
	return nil
}

// ListVehicles implements VehicleStore.
func (s *SQLiteEventStore) ListVehicles(ctx context.Context, campaignID string) ([]Vehicle, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, vehicle_type, stabled, location_id, created_at FROM vehicles
		 WHERE campaign_id = ?
		 ORDER BY created_at`,
		campaignID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing vehicles: %w", err)
	}
	defer rows.Close()

	var vehicles []Vehicle
	for rows.Next() {
		v := Vehicle{CampaignID: campaignID}
		var stabled int
		var createdAt string
		if err := rows.Scan(&v.ID, &v.Name, &v.VehicleType, &stabled, &v.LocationID, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scanning vehicle row: %w", err)
		}
		v.Stabled = stabled != 0
		v.CreatedAt, err = time.Parse(occurredAtLayout, createdAt)
		if err != nil {
			return nil, fmt.Errorf("store: parsing vehicle created_at: %w", err)
		}
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading vehicle rows: %w", err)
	}
	return vehicles, nil
}

// GetVehicle implements VehicleStore.
func (s *SQLiteEventStore) GetVehicle(ctx context.Context, campaignID, vehicleID string) (Vehicle, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, vehicle_type, stabled, location_id, created_at FROM vehicles
		 WHERE campaign_id = ? AND id = ?`,
		campaignID, vehicleID,
	)
	v := Vehicle{ID: vehicleID, CampaignID: campaignID}
	var stabled int
	var createdAt string
	err := row.Scan(&v.Name, &v.VehicleType, &stabled, &v.LocationID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Vehicle{}, false, nil
	}
	if err != nil {
		return Vehicle{}, false, fmt.Errorf("store: getting vehicle: %w", err)
	}
	v.Stabled = stabled != 0
	v.CreatedAt, err = time.Parse(occurredAtLayout, createdAt)
	if err != nil {
		return Vehicle{}, false, fmt.Errorf("store: parsing vehicle created_at: %w", err)
	}
	return v, true, nil
}

// StableVehicle implements VehicleStore.
func (s *SQLiteEventStore) StableVehicle(ctx context.Context, campaignID, vehicleID, locationID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE vehicles SET stabled = 1, location_id = ? WHERE campaign_id = ? AND id = ?`,
		locationID, campaignID, vehicleID,
	)
	if err != nil {
		return false, fmt.Errorf("store: stabling vehicle: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: stabling vehicle: %w", err)
	}
	return rows > 0, nil
}

// TakeVehicle implements VehicleStore.
func (s *SQLiteEventStore) TakeVehicle(ctx context.Context, campaignID, vehicleID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE vehicles SET stabled = 0, location_id = '' WHERE campaign_id = ? AND id = ?`,
		campaignID, vehicleID,
	)
	if err != nil {
		return false, fmt.Errorf("store: taking vehicle: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: taking vehicle: %w", err)
	}
	return rows > 0, nil
}
