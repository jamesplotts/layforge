// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"testing"
)

func TestSQLiteEventStore_CreateVehicle_StartsTravelingWithTheParty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateVehicle(ctx, "vehicle-1", "campaign-1", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}

	v, ok, err := s.GetVehicle(ctx, "campaign-1", "vehicle-1")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if !ok {
		t.Fatal("GetVehicle() ok = false, want true")
	}
	if v.Name != "Old Nag" || v.VehicleType != "mount" {
		t.Errorf("GetVehicle() = %+v, want Name=Old Nag VehicleType=mount", v)
	}
	if v.Stabled {
		t.Error("newly created vehicle Stabled = true, want false (starts traveling with the party)")
	}
}

func TestSQLiteEventStore_GetVehicle_NotFound_ReturnsNotOK(t *testing.T) {
	s := newTestStore(t)
	_, ok, err := s.GetVehicle(context.Background(), "campaign-1", "never-created")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if ok {
		t.Error("GetVehicle() ok = true, want false")
	}
}

func TestSQLiteEventStore_StableVehicle_Success_SetsStabledAndLocation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateVehicle(ctx, "vehicle-1", "campaign-1", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}

	found, err := s.StableVehicle(ctx, "campaign-1", "vehicle-1", "keep-stonewatch")
	if err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}
	if !found {
		t.Fatal("StableVehicle() found = false, want true")
	}

	v, _, err := s.GetVehicle(ctx, "campaign-1", "vehicle-1")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if !v.Stabled || v.LocationID != "keep-stonewatch" {
		t.Errorf("GetVehicle() = %+v, want Stabled=true LocationID=keep-stonewatch", v)
	}
}

func TestSQLiteEventStore_StableVehicle_NotFound_ReturnsFalse(t *testing.T) {
	s := newTestStore(t)
	found, err := s.StableVehicle(context.Background(), "campaign-1", "never-created", "keep-stonewatch")
	if err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}
	if found {
		t.Error("StableVehicle() found = true, want false")
	}
}

func TestSQLiteEventStore_TakeVehicle_Success_ClearsStabledAndLocation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateVehicle(ctx, "vehicle-1", "campaign-1", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}
	if _, err := s.StableVehicle(ctx, "campaign-1", "vehicle-1", "keep-stonewatch"); err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}

	found, err := s.TakeVehicle(ctx, "campaign-1", "vehicle-1")
	if err != nil {
		t.Fatalf("TakeVehicle() error = %v", err)
	}
	if !found {
		t.Fatal("TakeVehicle() found = false, want true")
	}

	v, _, err := s.GetVehicle(ctx, "campaign-1", "vehicle-1")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if v.Stabled || v.LocationID != "" {
		t.Errorf("GetVehicle() = %+v, want Stabled=false LocationID=\"\"", v)
	}
}

func TestSQLiteEventStore_TakeVehicle_NotFound_ReturnsFalse(t *testing.T) {
	s := newTestStore(t)
	found, err := s.TakeVehicle(context.Background(), "campaign-1", "never-created")
	if err != nil {
		t.Fatalf("TakeVehicle() error = %v", err)
	}
	if found {
		t.Error("TakeVehicle() found = true, want false")
	}
}

func TestSQLiteEventStore_ListVehicles_ScopedByCampaignID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateVehicle(ctx, "vehicle-a", "campaign-a", "Wagon", "cart"); err != nil {
		t.Fatalf("CreateVehicle(campaign-a) error = %v", err)
	}
	if err := s.CreateVehicle(ctx, "vehicle-b", "campaign-b", "The Gull", "ship"); err != nil {
		t.Fatalf("CreateVehicle(campaign-b) error = %v", err)
	}

	vehiclesA, err := s.ListVehicles(ctx, "campaign-a")
	if err != nil {
		t.Fatalf("ListVehicles(campaign-a) error = %v", err)
	}
	if len(vehiclesA) != 1 || vehiclesA[0].Name != "Wagon" {
		t.Errorf("ListVehicles(campaign-a) = %+v, want one Wagon", vehiclesA)
	}

	vehiclesB, err := s.ListVehicles(ctx, "campaign-b")
	if err != nil {
		t.Fatalf("ListVehicles(campaign-b) error = %v", err)
	}
	if len(vehiclesB) != 1 || vehiclesB[0].Name != "The Gull" {
		t.Errorf("ListVehicles(campaign-b) = %+v, want one The Gull", vehiclesB)
	}
}
