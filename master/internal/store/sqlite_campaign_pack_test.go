// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestSQLiteEventStore_SaveAndGetCampaignPack_RoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignPack(ctx, "campaign-1", "/packs/sable-ravine", "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, ok, err := s.GetCampaignPack(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetCampaignPack() error = %v", err)
	}
	if !ok {
		t.Fatal("GetCampaignPack() ok = false, want true")
	}
	if got.PackDir != "/packs/sable-ravine" || got.PackID != "sable-ravine" {
		t.Errorf("GetCampaignPack() = %+v, want PackDir=/packs/sable-ravine PackID=sable-ravine", got)
	}
	if got.LoadedAt.IsZero() {
		t.Error("LoadedAt is zero, want a real timestamp")
	}
}

func TestSQLiteEventStore_GetCampaignPack_NoBinding_ReturnsNotOK(t *testing.T) {
	s := newTestStore(t)
	_, ok, err := s.GetCampaignPack(context.Background(), "campaign-never-bound")
	if err != nil {
		t.Fatalf("GetCampaignPack() error = %v", err)
	}
	if ok {
		t.Error("GetCampaignPack() ok = true, want false")
	}
}

func TestSQLiteEventStore_SaveCampaignPack_SameCampaignOverwrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignPack(ctx, "campaign-1", "/packs/old", "old-pack"); err != nil {
		t.Fatalf("first SaveCampaignPack() error = %v", err)
	}
	if err := s.SaveCampaignPack(ctx, "campaign-1", "/packs/new", "new-pack"); err != nil {
		t.Fatalf("second SaveCampaignPack() error = %v", err)
	}

	got, _, err := s.GetCampaignPack(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetCampaignPack() error = %v", err)
	}
	if got.PackID != "new-pack" {
		t.Errorf("PackID = %q, want %q (overwrite should have applied)", got.PackID, "new-pack")
	}
}

func TestSQLiteEventStore_PartyLocation_UnsetThenSetThenOverwrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc, err := s.GetPartyLocation(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetPartyLocation() error = %v", err)
	}
	if loc != "" {
		t.Errorf("GetPartyLocation() (unset) = %q, want empty", loc)
	}

	if err := s.SetPartyLocation(ctx, "campaign-1", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}
	loc, err = s.GetPartyLocation(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetPartyLocation() error = %v", err)
	}
	if loc != "keep-stonewatch" {
		t.Errorf("GetPartyLocation() = %q, want %q", loc, "keep-stonewatch")
	}

	if err := s.SetPartyLocation(ctx, "campaign-1", "old-road"); err != nil {
		t.Fatalf("SetPartyLocation() (move) error = %v", err)
	}
	loc, err = s.GetPartyLocation(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetPartyLocation() error = %v", err)
	}
	if loc != "old-road" {
		t.Errorf("GetPartyLocation() after move = %q, want %q", loc, "old-road")
	}
}

func TestSQLiteEventStore_LocationState_DiscoveredAndClaimedIndependently(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, ok, err := s.GetLocationState(ctx, "campaign-1", "old-road")
	if err != nil {
		t.Fatalf("GetLocationState() error = %v", err)
	}
	if ok {
		t.Error("GetLocationState() (never touched) ok = true, want false")
	}

	if err := s.SetLocationDiscovered(ctx, "campaign-1", "old-road"); err != nil {
		t.Fatalf("SetLocationDiscovered() error = %v", err)
	}
	state, ok, err := s.GetLocationState(ctx, "campaign-1", "old-road")
	if err != nil {
		t.Fatalf("GetLocationState() error = %v", err)
	}
	if !ok || !state.Discovered || state.ClaimedByParty {
		t.Errorf("GetLocationState() after discover = %+v, want Discovered=true ClaimedByParty=false", state)
	}

	if err := s.SetLocationClaimed(ctx, "campaign-1", "old-road", "the party's first foothold"); err != nil {
		t.Fatalf("SetLocationClaimed() error = %v", err)
	}
	state, _, err = s.GetLocationState(ctx, "campaign-1", "old-road")
	if err != nil {
		t.Fatalf("GetLocationState() error = %v", err)
	}
	if !state.Discovered || !state.ClaimedByParty || state.ClaimNote != "the party's first foothold" {
		t.Errorf("GetLocationState() after claim = %+v, want Discovered=true ClaimedByParty=true ClaimNote set", state)
	}
}

func TestSQLiteEventStore_StashAndRetrieveItem_RoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.StashItem(ctx, "stash-1", "campaign-1", "keep-stonewatch", "char-a", "Longsword"); err != nil {
		t.Fatalf("StashItem() error = %v", err)
	}

	items, err := s.ListStashedItems(ctx, "campaign-1", "keep-stonewatch")
	if err != nil {
		t.Fatalf("ListStashedItems() error = %v", err)
	}
	if len(items) != 1 || items[0].ItemName != "Longsword" || items[0].CharacterID != "char-a" {
		t.Fatalf("ListStashedItems() = %+v, want one Longsword stashed by char-a", items)
	}

	found, err := s.RetrieveItem(ctx, "campaign-1", "keep-stonewatch", "char-a", "Longsword")
	if err != nil {
		t.Fatalf("RetrieveItem() error = %v", err)
	}
	if !found {
		t.Fatal("RetrieveItem() found = false, want true")
	}

	items, err = s.ListStashedItems(ctx, "campaign-1", "keep-stonewatch")
	if err != nil {
		t.Fatalf("ListStashedItems() after retrieve error = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("ListStashedItems() after retrieve = %+v, want empty", items)
	}
}

func TestSQLiteEventStore_RetrieveItem_NothingStashedThere_ReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	found, err := s.RetrieveItem(context.Background(), "campaign-1", "old-road", "char-a", "Longsword")
	if err != nil {
		t.Fatalf("RetrieveItem() error = %v", err)
	}
	if found {
		t.Error("RetrieveItem() found = true, want false")
	}
}

func TestSQLiteEventStore_RetrieveItem_WrongLocation_ReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.StashItem(ctx, "stash-1", "campaign-1", "keep-stonewatch", "char-a", "Longsword"); err != nil {
		t.Fatalf("StashItem() error = %v", err)
	}

	// Stashed at keep-stonewatch — retrieving from a different location
	// (the party isn't there) must fail, not silently succeed from
	// wherever they actually are.
	found, err := s.RetrieveItem(ctx, "campaign-1", "old-road", "char-a", "Longsword")
	if err != nil {
		t.Fatalf("RetrieveItem() error = %v", err)
	}
	if found {
		t.Error("RetrieveItem() from the wrong location found = true, want false")
	}
}

func TestSQLiteEventStore_StashedCurrency_DepositsAccumulateAdditively(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AddStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 10, 0); err != nil {
		t.Fatalf("first AddStashedCurrency() error = %v", err)
	}
	if err := s.AddStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 5, 0); err != nil {
		t.Fatalf("second AddStashedCurrency() error = %v", err)
	}

	_, _, gold, _, err := s.GetStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a")
	if err != nil {
		t.Fatalf("GetStashedCurrency() error = %v", err)
	}
	if gold != 15 {
		t.Errorf("GetStashedCurrency() gold = %d, want 15 (two deposits should accumulate, not overwrite)", gold)
	}
}

func TestSQLiteEventStore_RemoveStashedCurrency_Success_DecrementsBalance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AddStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 15, 0); err != nil {
		t.Fatalf("AddStashedCurrency() error = %v", err)
	}

	if err := s.RemoveStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 10, 0); err != nil {
		t.Fatalf("RemoveStashedCurrency() error = %v", err)
	}

	_, _, gold, _, err := s.GetStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a")
	if err != nil {
		t.Fatalf("GetStashedCurrency() error = %v", err)
	}
	if gold != 5 {
		t.Errorf("GetStashedCurrency() gold = %d, want 5", gold)
	}
}

func TestSQLiteEventStore_RemoveStashedCurrency_InsufficientDenomination_ReturnsErrorAndLeavesBalanceUnchanged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AddStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 5, 0); err != nil {
		t.Fatalf("AddStashedCurrency() error = %v", err)
	}

	err := s.RemoveStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a", 0, 0, 10, 0)
	if !errors.Is(err, store.ErrInsufficientStashedCurrency) {
		t.Errorf("RemoveStashedCurrency() error = %v, want ErrInsufficientStashedCurrency", err)
	}

	_, _, gold, _, err := s.GetStashedCurrency(ctx, "campaign-1", "keep-stonewatch", "char-a")
	if err != nil {
		t.Fatalf("GetStashedCurrency() error = %v", err)
	}
	if gold != 5 {
		t.Errorf("GetStashedCurrency() gold = %d, want unchanged 5 after a rejected withdrawal", gold)
	}
}

func TestSQLiteEventStore_CampaignPackData_ScopedByCampaignID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetPartyLocation(ctx, "campaign-a", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation(campaign-a) error = %v", err)
	}
	if err := s.SetPartyLocation(ctx, "campaign-b", "old-road"); err != nil {
		t.Fatalf("SetPartyLocation(campaign-b) error = %v", err)
	}
	if err := s.StashItem(ctx, "stash-a", "campaign-a", "keep-stonewatch", "char-a", "Longsword"); err != nil {
		t.Fatalf("StashItem(campaign-a) error = %v", err)
	}

	locA, err := s.GetPartyLocation(ctx, "campaign-a")
	if err != nil || locA != "keep-stonewatch" {
		t.Errorf("GetPartyLocation(campaign-a) = %q, %v, want keep-stonewatch, nil", locA, err)
	}
	locB, err := s.GetPartyLocation(ctx, "campaign-b")
	if err != nil || locB != "old-road" {
		t.Errorf("GetPartyLocation(campaign-b) = %q, %v, want old-road, nil", locB, err)
	}

	itemsB, err := s.ListStashedItems(ctx, "campaign-b", "keep-stonewatch")
	if err != nil {
		t.Fatalf("ListStashedItems(campaign-b) error = %v", err)
	}
	if len(itemsB) != 0 {
		t.Errorf("ListStashedItems(campaign-b) = %+v, want empty (campaign-a's stash must not leak)", itemsB)
	}
}
