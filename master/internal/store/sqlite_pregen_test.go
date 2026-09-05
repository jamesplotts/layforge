// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func testPregen(id, campaignID string) store.Pregen {
	return store.Pregen{
		ID:            id,
		CampaignID:    campaignID,
		Name:          "Bram the Bold",
		Description:   "A stalwart level-1 fighter, ready to go.",
		SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Bram"}`),
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}
}

func TestSQLiteEventStore_SaveAndGetPregen_RoundTripsAllFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := testPregen("pregen-1", "campaign-1")

	if err := s.SavePregen(ctx, p); err != nil {
		t.Fatalf("SavePregen() error = %v", err)
	}

	got, err := s.GetPregen(ctx, "pregen-1")
	if err != nil {
		t.Fatalf("GetPregen() error = %v", err)
	}
	if got.ID != p.ID || got.CampaignID != p.CampaignID || got.Name != p.Name ||
		got.Description != p.Description || got.SchemaVersion != p.SchemaVersion {
		t.Errorf("GetPregen() = %+v, want %+v", got, p)
	}
	if string(got.CharacterData) != string(p.CharacterData) {
		t.Errorf("CharacterData = %s, want %s", got.CharacterData, p.CharacterData)
	}
	if !got.CreatedAt.Equal(p.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, p.CreatedAt)
	}
}

func TestSQLiteEventStore_SavePregen_SameIDOverwrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := testPregen("pregen-1", "campaign-1")
	if err := s.SavePregen(ctx, p); err != nil {
		t.Fatalf("SavePregen() error = %v", err)
	}

	p.Name = "Renamed"
	if err := s.SavePregen(ctx, p); err != nil {
		t.Fatalf("SavePregen() (overwrite) error = %v", err)
	}

	got, err := s.GetPregen(ctx, "pregen-1")
	if err != nil {
		t.Fatalf("GetPregen() error = %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("GetPregen().Name = %q, want %q", got.Name, "Renamed")
	}
}

func TestSQLiteEventStore_GetPregen_NotFound_ReturnsErrPregenNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetPregen(context.Background(), "no-such-pregen")
	if !errors.Is(err, store.ErrPregenNotFound) {
		t.Errorf("GetPregen() error = %v, want ErrPregenNotFound", err)
	}
}

func TestSQLiteEventStore_SavePregen_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	p := testPregen("pregen-1", "")
	if err := s.SavePregen(context.Background(), p); !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("SavePregen() error = %v, want ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_SavePregen_MissingID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	p := testPregen("", "campaign-1")
	if err := s.SavePregen(context.Background(), p); !errors.Is(err, store.ErrPregenIDRequired) {
		t.Errorf("SavePregen() error = %v, want ErrPregenIDRequired", err)
	}
}

func TestSQLiteEventStore_ListPregens_ReturnsOnlyThatCampaignsPregens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p1 := testPregen("pregen-1", "campaign-1")
	p2 := testPregen("pregen-2", "campaign-1")
	other := testPregen("pregen-3", "campaign-2")
	for _, p := range []store.Pregen{p1, p2, other} {
		if err := s.SavePregen(ctx, p); err != nil {
			t.Fatalf("SavePregen(%s) error = %v", p.ID, err)
		}
	}

	got, err := s.ListPregens(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("ListPregens() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListPregens() returned %d pregens, want 2 (got: %+v)", len(got), got)
	}
	ids := map[string]bool{}
	for _, p := range got {
		ids[p.ID] = true
	}
	if !ids["pregen-1"] || !ids["pregen-2"] {
		t.Errorf("ListPregens() ids = %v, want pregen-1 and pregen-2", ids)
	}
	if ids["pregen-3"] {
		t.Error("ListPregens() included pregen-3, which belongs to a different campaign")
	}
}

func TestSQLiteEventStore_ListPregens_NoPregens_ReturnsEmptySliceNotError(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ListPregens(context.Background(), "campaign-empty")
	if err != nil {
		t.Fatalf("ListPregens() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPregens() = %+v, want empty", got)
	}
}

func TestSQLiteEventStore_DeletePregen_RemovesIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := testPregen("pregen-1", "campaign-1")
	if err := s.SavePregen(ctx, p); err != nil {
		t.Fatalf("SavePregen() error = %v", err)
	}

	if err := s.DeletePregen(ctx, "pregen-1"); err != nil {
		t.Fatalf("DeletePregen() error = %v", err)
	}

	if _, err := s.GetPregen(ctx, "pregen-1"); !errors.Is(err, store.ErrPregenNotFound) {
		t.Errorf("GetPregen() after delete error = %v, want ErrPregenNotFound", err)
	}
}

func TestSQLiteEventStore_DeletePregen_UnknownID_NoError(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeletePregen(context.Background(), "never-existed"); err != nil {
		t.Errorf("DeletePregen() error = %v, want nil for a no-op delete", err)
	}
}
