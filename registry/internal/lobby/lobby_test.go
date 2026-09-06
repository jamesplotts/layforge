// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package lobby_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/registry/internal/lobby"
)

func testFields() lobby.Fields {
	return lobby.Fields{
		AdventureName:     "The Sable Ravine",
		MinLevel:          1,
		MaxLevel:          3,
		PlayersJoined:     2,
		PlayerSlots:       5,
		PasswordProtected: false,
		JoinURL:           "wss://example.com/ws",
		CampaignID:        "sable-ravine",
	}
}

func TestStore_CreateThenLive_ReturnsTheListing(t *testing.T) {
	s := lobby.NewStore()
	id, token, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if id == "" || token == "" {
		t.Fatalf("Create() id=%q token=%q, want both non-empty", id, token)
	}

	live := s.Live(time.Minute)
	if len(live) != 1 {
		t.Fatalf("Live() returned %d listings, want 1", len(live))
	}
	got := live[0]
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.AdventureName != "The Sable Ravine" {
		t.Errorf("AdventureName = %q, want %q", got.AdventureName, "The Sable Ravine")
	}
	if got.MinLevel != 1 || got.MaxLevel != 3 {
		t.Errorf("MinLevel/MaxLevel = %d/%d, want 1/3", got.MinLevel, got.MaxLevel)
	}
	if got.PlayersJoined != 2 || got.PlayerSlots != 5 {
		t.Errorf("PlayersJoined/PlayerSlots = %d/%d, want 2/5", got.PlayersJoined, got.PlayerSlots)
	}
	if got.JoinURL != "wss://example.com/ws" || got.CampaignID != "sable-ravine" {
		t.Errorf("JoinURL/CampaignID = %q/%q, want wss://example.com/ws/sable-ravine", got.JoinURL, got.CampaignID)
	}
}

func TestStore_Live_NeverExposesTheToken(t *testing.T) {
	s := lobby.NewStore()
	_, token, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	live := s.Live(time.Minute)
	if len(live) != 1 {
		t.Fatalf("Live() returned %d listings, want 1", len(live))
	}
	if live[0].Token != "" {
		t.Errorf("Live() exposed Token = %q, want empty — the token must never leave Create's own return value", live[0].Token)
	}
	_ = token
}

func TestStore_Create_DistinctListingsGetDistinctIDsAndTokens(t *testing.T) {
	s := lobby.NewStore()
	id1, token1, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	id2, token2, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if id1 == id2 {
		t.Errorf("both Create() calls returned the same id %q", id1)
	}
	if token1 == token2 {
		t.Errorf("both Create() calls returned the same token %q", token1)
	}
}

func TestStore_Heartbeat_UpdatesFieldsAndRefreshesLastHeartbeat(t *testing.T) {
	s := lobby.NewStore()
	id, token, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated := testFields()
	updated.PlayersJoined = 4
	if err := s.Heartbeat(id, token, updated); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	live := s.Live(time.Minute)
	if len(live) != 1 {
		t.Fatalf("Live() returned %d listings, want 1", len(live))
	}
	if live[0].PlayersJoined != 4 {
		t.Errorf("PlayersJoined = %d, want 4 (heartbeat should update it)", live[0].PlayersJoined)
	}
}

func TestStore_Heartbeat_UnknownID_ReturnsErrNotFound(t *testing.T) {
	s := lobby.NewStore()
	err := s.Heartbeat("does-not-exist", "any-token", testFields())
	if !errors.Is(err, lobby.ErrNotFound) {
		t.Errorf("Heartbeat() error = %v, want ErrNotFound", err)
	}
}

func TestStore_Heartbeat_WrongToken_ReturnsErrTokenMismatch(t *testing.T) {
	s := lobby.NewStore()
	id, _, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := s.Heartbeat(id, "wrong-token", testFields()); !errors.Is(err, lobby.ErrTokenMismatch) {
		t.Errorf("Heartbeat() error = %v, want ErrTokenMismatch", err)
	}
}

func TestStore_Remove_DeletesTheListing(t *testing.T) {
	s := lobby.NewStore()
	id, token, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := s.Remove(id, token); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if live := s.Live(time.Minute); len(live) != 0 {
		t.Errorf("Live() returned %d listings after Remove(), want 0", len(live))
	}
}

func TestStore_Remove_WrongToken_ReturnsErrTokenMismatch_AndDoesNotDelete(t *testing.T) {
	s := lobby.NewStore()
	id, _, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := s.Remove(id, "wrong-token"); !errors.Is(err, lobby.ErrTokenMismatch) {
		t.Errorf("Remove() error = %v, want ErrTokenMismatch", err)
	}
	if live := s.Live(time.Minute); len(live) != 1 {
		t.Errorf("Live() returned %d listings after a rejected Remove(), want 1 (must not have been deleted)", len(live))
	}
}

func TestStore_Remove_UnknownID_ReturnsErrNotFound(t *testing.T) {
	s := lobby.NewStore()
	if err := s.Remove("does-not-exist", "any-token"); !errors.Is(err, lobby.ErrNotFound) {
		t.Errorf("Remove() error = %v, want ErrNotFound", err)
	}
}

func TestStore_Live_ExcludesListingsPastTTL(t *testing.T) {
	s := lobby.NewStore()
	id, _, err := s.Create(testFields())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A zero TTL means "must have heartbeated in the last instant" —
	// this listing's LastHeartbeat is already in the past by the time
	// Live() computes its cutoff, so it must be excluded.
	time.Sleep(2 * time.Millisecond)
	live := s.Live(time.Millisecond)
	if len(live) != 0 {
		t.Errorf("Live(1ms) returned %d listings for one created 2ms ago, want 0 (past TTL)", len(live))
	}
	_ = id
}

func TestStore_Sweep_DeletesListingsPastTTL(t *testing.T) {
	s := lobby.NewStore()
	if _, _, err := s.Create(testFields()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	s.Sweep(time.Millisecond)
	// Even with an unbounded TTL, the entry should be gone now — Sweep
	// actually deletes, not just filters a read.
	if live := s.Live(time.Hour); len(live) != 0 {
		t.Errorf("Live(1h) returned %d listings after Sweep(1ms), want 0 (Sweep should have deleted it)", len(live))
	}
}

func TestStore_Sweep_KeepsListingsWithinTTL(t *testing.T) {
	s := lobby.NewStore()
	if _, _, err := s.Create(testFields()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	s.Sweep(time.Hour)
	if live := s.Live(time.Hour); len(live) != 1 {
		t.Errorf("Live(1h) returned %d listings after Sweep(1h) on a fresh listing, want 1", len(live))
	}
}
