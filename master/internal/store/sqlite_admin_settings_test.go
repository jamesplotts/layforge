// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestSQLiteEventStore_GetCampaignSettings_NoRow_ReturnsNotOK(t *testing.T) {
	s := newTestStore(t)

	settings, ok, err := s.GetCampaignSettings(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetCampaignSettings() error = %v", err)
	}
	if ok {
		t.Errorf("GetCampaignSettings() ok = true, want false for a campaign never saved")
	}
	if settings.PvPPolicy != "" || settings.PvPConsent != nil || settings.MaturityTierPrompt != "" ||
		settings.ImageMaturityTierPrompt != "" || settings.RoomPassword != "" {
		t.Errorf("GetCampaignSettings() settings = %+v, want zero value", settings)
	}
}

func TestSQLiteEventStore_SaveAndGetCampaignSettings_RoundTripsAllFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	want := store.CampaignSettings{
		PvPPolicy:               "pvp_with_consent",
		PvPConsent:              []string{"player-a", "player-b"},
		MaturityTierPrompt:      "Keep content suitable for all ages.",
		ImageMaturityTierPrompt: "No graphic violence in illustrations.",
		RoomPassword:            "hunter2",
	}

	if err := s.SaveCampaignSettings(ctx, "campaign-1", want); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	got, ok, err := s.GetCampaignSettings(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetCampaignSettings() error = %v", err)
	}
	if !ok {
		t.Fatal("GetCampaignSettings() ok = false, want true")
	}
	if got.PvPPolicy != want.PvPPolicy || got.MaturityTierPrompt != want.MaturityTierPrompt ||
		got.ImageMaturityTierPrompt != want.ImageMaturityTierPrompt || got.RoomPassword != want.RoomPassword {
		t.Errorf("GetCampaignSettings() = %+v, want %+v", got, want)
	}
	if len(got.PvPConsent) != len(want.PvPConsent) {
		t.Fatalf("PvPConsent = %v, want %v", got.PvPConsent, want.PvPConsent)
	}
	for i := range want.PvPConsent {
		if got.PvPConsent[i] != want.PvPConsent[i] {
			t.Errorf("PvPConsent[%d] = %q, want %q", i, got.PvPConsent[i], want.PvPConsent[i])
		}
	}
}

func TestSQLiteEventStore_SaveCampaignSettings_SameCampaignOverwrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{PvPPolicy: "pve_only"}); err != nil {
		t.Fatalf("first SaveCampaignSettings() error = %v", err)
	}
	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{PvPPolicy: "pvp_allowed"}); err != nil {
		t.Fatalf("second SaveCampaignSettings() error = %v", err)
	}

	got, ok, err := s.GetCampaignSettings(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("GetCampaignSettings() error = %v", err)
	}
	if !ok {
		t.Fatal("GetCampaignSettings() ok = false, want true")
	}
	if got.PvPPolicy != "pvp_allowed" {
		t.Errorf("PvPPolicy = %q, want %q (overwrite should have applied)", got.PvPPolicy, "pvp_allowed")
	}
}

func TestSQLiteEventStore_SaveCampaignSettings_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)

	err := s.SaveCampaignSettings(context.Background(), "", store.CampaignSettings{})
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("SaveCampaignSettings() error = %v, want ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_ListCampaignIDs_UnionsEventsCharactersAndSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AppendEvent(ctx, testEvent("campaign-events", "msg-1")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-characters")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.SaveCampaignSettings(ctx, "campaign-settings-only", store.CampaignSettings{PvPPolicy: "pve_only"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}
	// A campaign with both events and settings should appear once, not twice.
	if err := s.AppendEvent(ctx, testEvent("campaign-settings-only", "msg-2")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	got, err := s.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs() error = %v", err)
	}

	want := []string{"campaign-characters", "campaign-events", "campaign-settings-only"}
	if len(got) != len(want) {
		t.Fatalf("ListCampaignIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListCampaignIDs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSQLiteEventStore_GetSystemSettings_NoneSaved_ReturnsEmptyMap(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSystemSettings() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GetSystemSettings() = %v, want empty map", got)
	}
}

func TestSQLiteEventStore_SaveAndGetSystemSettings_RoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveSystemSettings(ctx, map[string]string{"llm_url": "http://host:11434", "llm_model": "qwen2.5:32b"}); err != nil {
		t.Fatalf("SaveSystemSettings() error = %v", err)
	}

	got, err := s.GetSystemSettings(ctx)
	if err != nil {
		t.Fatalf("GetSystemSettings() error = %v", err)
	}
	if got["llm_url"] != "http://host:11434" || got["llm_model"] != "qwen2.5:32b" {
		t.Errorf("GetSystemSettings() = %v, want llm_url/llm_model set", got)
	}
}

func TestSQLiteEventStore_SaveSystemSettings_PartialUpdateLeavesOtherKeysUntouched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveSystemSettings(ctx, map[string]string{"llm_url": "http://host-a:11434", "llm_model": "model-a"}); err != nil {
		t.Fatalf("first SaveSystemSettings() error = %v", err)
	}
	if err := s.SaveSystemSettings(ctx, map[string]string{"llm_model": "model-b"}); err != nil {
		t.Fatalf("second SaveSystemSettings() error = %v", err)
	}

	got, err := s.GetSystemSettings(ctx)
	if err != nil {
		t.Fatalf("GetSystemSettings() error = %v", err)
	}
	if got["llm_url"] != "http://host-a:11434" {
		t.Errorf("llm_url = %q, want unchanged %q", got["llm_url"], "http://host-a:11434")
	}
	if got["llm_model"] != "model-b" {
		t.Errorf("llm_model = %q, want updated %q", got["llm_model"], "model-b")
	}
}
