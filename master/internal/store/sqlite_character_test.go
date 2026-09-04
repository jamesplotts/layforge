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

func testCharacter(id, campaignID string) store.Character {
	now := time.Now().UTC().Truncate(time.Second)
	return store.Character{
		ID:            id,
		CampaignID:    campaignID,
		OwnerID:       "sender-1",
		SchemaVersion: "opencombatengine-v1",
		Status:        store.CharacterStatusPendingReview,
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func TestSQLiteEventStore_SaveAndGetCharacter_RoundTripsAllFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	c := testCharacter("char-1", "campaign-1")

	if err := s.SaveCharacter(ctx, c); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}

	got, err := s.GetCharacter(ctx, "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if got.ID != c.ID || got.CampaignID != c.CampaignID || got.OwnerID != c.OwnerID ||
		got.SchemaVersion != c.SchemaVersion || got.Status != c.Status {
		t.Errorf("GetCharacter() = %+v, want %+v", got, c)
	}
	if string(got.CharacterData) != string(c.CharacterData) {
		t.Errorf("CharacterData = %s, want %s", got.CharacterData, c.CharacterData)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, c.CreatedAt)
	}
	if !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, c.UpdatedAt)
	}
}

func TestSQLiteEventStore_ListCharacters_ReturnsOnlyThatCampaignsCharacters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	c1 := testCharacter("char-1", "campaign-1")
	c2 := testCharacter("char-2", "campaign-1")
	c2.OwnerID = "sender-2"
	other := testCharacter("char-3", "campaign-2")
	for _, c := range []store.Character{c1, c2, other} {
		if err := s.SaveCharacter(ctx, c); err != nil {
			t.Fatalf("SaveCharacter(%s) error = %v", c.ID, err)
		}
	}

	got, err := s.ListCharacters(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("ListCharacters() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListCharacters() returned %d characters, want 2 (got: %+v)", len(got), got)
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	if !ids["char-1"] || !ids["char-2"] {
		t.Errorf("ListCharacters() ids = %v, want char-1 and char-2", ids)
	}
	if ids["char-3"] {
		t.Error("ListCharacters() included char-3, which belongs to a different campaign")
	}
}

func TestSQLiteEventStore_ListCharacters_NoCharacters_ReturnsEmptySliceNotError(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ListCharacters(context.Background(), "campaign-empty")
	if err != nil {
		t.Fatalf("ListCharacters() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListCharacters() = %+v, want empty", got)
	}
}

func TestSQLiteEventStore_SaveCharacter_SameIDOverwrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	c := testCharacter("char-1", "campaign-1")

	if err := s.SaveCharacter(ctx, c); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}

	c.Status = store.CharacterStatusApproved
	c.CharacterData = json.RawMessage(`{"name":"Kestrel","level":2}`)
	if err := s.SaveCharacter(ctx, c); err != nil {
		t.Fatalf("second SaveCharacter() error = %v", err)
	}

	got, err := s.GetCharacter(ctx, "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if got.Status != store.CharacterStatusApproved {
		t.Errorf("Status = %q, want %q (overwrite should have applied)", got.Status, store.CharacterStatusApproved)
	}
	if string(got.CharacterData) != `{"name":"Kestrel","level":2}` {
		t.Errorf("CharacterData = %s, want updated value", got.CharacterData)
	}
}

func TestSQLiteEventStore_SaveCharacter_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	c := testCharacter("char-1", "")

	err := s.SaveCharacter(context.Background(), c)
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("SaveCharacter() error = %v, want ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_SaveCharacter_MissingCharacterID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	c := testCharacter("", "campaign-1")

	err := s.SaveCharacter(context.Background(), c)
	if !errors.Is(err, store.ErrCharacterIDRequired) {
		t.Errorf("SaveCharacter() error = %v, want ErrCharacterIDRequired", err)
	}
}

func TestSQLiteEventStore_GetCharacter_NotFound_ReturnsError(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetCharacter(context.Background(), "does-not-exist")
	if !errors.Is(err, store.ErrCharacterNotFound) {
		t.Errorf("GetCharacter() error = %v, want ErrCharacterNotFound", err)
	}
}
