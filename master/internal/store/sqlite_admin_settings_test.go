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
		PriceMultiplier:         1.5,
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
		got.ImageMaturityTierPrompt != want.ImageMaturityTierPrompt || got.RoomPassword != want.RoomPassword ||
		got.PriceMultiplier != want.PriceMultiplier {
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

func TestSQLiteEventStore_ListCampaignSummaries_PartyCountAndLastActiveAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-active")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.SaveCharacter(ctx, testCharacter("char-2", "campaign-active")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-active", "msg-1")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	var got *store.CampaignSummary
	for i := range summaries {
		if summaries[i].CampaignID == "campaign-active" {
			got = &summaries[i]
		}
	}
	if got == nil {
		t.Fatal("ListCampaignSummaries() did not include campaign-active")
	}
	if got.PartyCount != 2 {
		t.Errorf("PartyCount = %d, want 2", got.PartyCount)
	}
	if got.LastActiveAt.IsZero() {
		t.Error("LastActiveAt is zero, want a real timestamp from the appended event")
	}
	if got.DisplayName != "" {
		t.Errorf("DisplayName = %q, want empty (never named)", got.DisplayName)
	}
	if got.Archived {
		t.Error("Archived = true, want false (default)")
	}
}

func TestSQLiteEventStore_ListCampaignSummaries_CharactersOnlyNoEvents_UsesCharacterUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-uploaded-only")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	var got *store.CampaignSummary
	for i := range summaries {
		if summaries[i].CampaignID == "campaign-uploaded-only" {
			got = &summaries[i]
		}
	}
	if got == nil {
		t.Fatal("ListCampaignSummaries() did not include campaign-uploaded-only")
	}
	if got.LastActiveAt.IsZero() {
		t.Error("LastActiveAt is zero, want the character's own updated_at as a fallback")
	}
}

func TestSQLiteEventStore_SaveCampaignMeta_NewCampaign_ZeroPartyCountAndNoActivity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignMeta(ctx, "campaign-fresh", "Friday Night Crew"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	got := summaries[0]
	if got.CampaignID != "campaign-fresh" {
		t.Errorf("CampaignID = %q, want %q", got.CampaignID, "campaign-fresh")
	}
	if got.DisplayName != "Friday Night Crew" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Friday Night Crew")
	}
	if got.PartyCount != 0 {
		t.Errorf("PartyCount = %d, want 0 (nobody has joined yet)", got.PartyCount)
	}
	if !got.LastActiveAt.IsZero() {
		t.Errorf("LastActiveAt = %v, want zero (nobody has joined yet)", got.LastActiveAt)
	}
}

func TestSQLiteEventStore_SaveCampaignMeta_AlreadyActiveCampaign_PreservesActivity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-already-active")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-already-active", "msg-1")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	if err := s.SaveCampaignMeta(ctx, "campaign-already-active", "The Iron Crown"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	var got *store.CampaignSummary
	for i := range summaries {
		if summaries[i].CampaignID == "campaign-already-active" {
			got = &summaries[i]
		}
	}
	if got == nil {
		t.Fatal("ListCampaignSummaries() did not include campaign-already-active")
	}
	if got.DisplayName != "The Iron Crown" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "The Iron Crown")
	}
	if got.PartyCount != 1 {
		t.Errorf("PartyCount = %d, want 1 (naming shouldn't touch real activity)", got.PartyCount)
	}
	if got.LastActiveAt.IsZero() {
		t.Error("LastActiveAt is zero, want the real event timestamp to survive naming")
	}
}

func TestSQLiteEventStore_SaveCampaignMeta_Rename_DoesNotUnarchive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetCampaignArchived(ctx, "campaign-1", true); err != nil {
		t.Fatalf("SetCampaignArchived() error = %v", err)
	}
	if err := s.SaveCampaignMeta(ctx, "campaign-1", "Renamed Campaign"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if !summaries[0].Archived {
		t.Error("Archived = false, want true — naming an archived campaign should not silently unarchive it")
	}
	if summaries[0].DisplayName != "Renamed Campaign" {
		t.Errorf("DisplayName = %q, want %q", summaries[0].DisplayName, "Renamed Campaign")
	}
}

func TestSQLiteEventStore_SaveCampaignMeta_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	err := s.SaveCampaignMeta(context.Background(), "", "name")
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("SaveCampaignMeta() error = %v, want ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_SetCampaignArchived_TogglesAndPreservesDisplayName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignMeta(ctx, "campaign-1", "Original Name"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}
	if err := s.SetCampaignArchived(ctx, "campaign-1", true); err != nil {
		t.Fatalf("SetCampaignArchived(true) error = %v", err)
	}

	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	if len(summaries) != 1 || !summaries[0].Archived || summaries[0].DisplayName != "Original Name" {
		t.Fatalf("after archiving: %+v, want Archived=true DisplayName=%q", summaries[0], "Original Name")
	}

	if err := s.SetCampaignArchived(ctx, "campaign-1", false); err != nil {
		t.Fatalf("SetCampaignArchived(false) error = %v", err)
	}
	summaries, err = s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].Archived || summaries[0].DisplayName != "Original Name" {
		t.Fatalf("after unarchiving: %+v, want Archived=false DisplayName=%q", summaries[0], "Original Name")
	}
}

func TestSQLiteEventStore_SetCampaignArchived_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	err := s.SetCampaignArchived(context.Background(), "", true)
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("SetCampaignArchived() error = %v, want ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_DeleteCampaign_NotArchived_ReturnsErrorAndDeletesNothing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-live")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-live", "msg-1")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	err := s.DeleteCampaign(ctx, "campaign-live")
	if !errors.Is(err, store.ErrCampaignNotArchived) {
		t.Errorf("DeleteCampaign() error = %v, want ErrCampaignNotArchived", err)
	}

	if _, err := s.GetCharacter(ctx, "char-1"); err != nil {
		t.Errorf("GetCharacter() error = %v, want the character to still exist", err)
	}
	events, _, err := s.ListEvents(ctx, "campaign-live", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Errorf("len(events) = %d, want 1 (nothing should have been deleted)", len(events))
	}
}

func TestSQLiteEventStore_DeleteCampaign_NeverArchived_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteCampaign(context.Background(), "campaign-never-touched")
	if !errors.Is(err, store.ErrCampaignNotArchived) {
		t.Errorf("DeleteCampaign() error = %v, want ErrCampaignNotArchived", err)
	}
}

func TestSQLiteEventStore_DeleteCampaign_Archived_RemovesEveryTableAndLeavesOtherCampaignsAlone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// The campaign being deleted: a real row in every table that
	// references campaign_id.
	if err := s.SaveCharacter(ctx, testCharacter("char-1", "campaign-doomed")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-doomed", "msg-1")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := s.SaveCampaignSettings(ctx, "campaign-doomed", store.CampaignSettings{PvPPolicy: "pve_only"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}
	if err := s.SaveCombatState(ctx, "campaign-doomed", []byte(`{"turn_order":{"active":true}}`)); err != nil {
		t.Fatalf("SaveCombatState() error = %v", err)
	}
	if err := s.SaveCampaignMeta(ctx, "campaign-doomed", "Doomed Campaign"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}
	if err := s.SetCampaignArchived(ctx, "campaign-doomed", true); err != nil {
		t.Fatalf("SetCampaignArchived() error = %v", err)
	}

	// A second, untouched campaign with rows in the same tables — proves
	// the DELETEs are scoped by campaign_id, not a wholesale wipe.
	if err := s.SaveCharacter(ctx, testCharacter("char-2", "campaign-survivor")); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-survivor", "msg-2")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := s.SaveCampaignSettings(ctx, "campaign-survivor", store.CampaignSettings{PvPPolicy: "pvp_allowed"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	if err := s.DeleteCampaign(ctx, "campaign-doomed"); err != nil {
		t.Fatalf("DeleteCampaign() error = %v", err)
	}

	if _, err := s.GetCharacter(ctx, "char-1"); !errors.Is(err, store.ErrCharacterNotFound) {
		t.Errorf("GetCharacter(char-1) error = %v, want ErrCharacterNotFound", err)
	}
	if events, _, err := s.ListEvents(ctx, "campaign-doomed", store.ListEventsOptions{}); err != nil || len(events) != 0 {
		t.Errorf("ListEvents(campaign-doomed) = %v, %v, want empty", events, err)
	}
	if _, ok, err := s.GetCampaignSettings(ctx, "campaign-doomed"); err != nil || ok {
		t.Errorf("GetCampaignSettings(campaign-doomed) ok = %v, err = %v, want ok=false", ok, err)
	}
	if _, ok, err := s.LoadCombatState(ctx, "campaign-doomed"); err != nil || ok {
		t.Errorf("LoadCombatState(campaign-doomed) ok = %v, err = %v, want ok=false", ok, err)
	}
	summaries, err := s.ListCampaignSummaries(ctx)
	if err != nil {
		t.Fatalf("ListCampaignSummaries() error = %v", err)
	}
	for _, summary := range summaries {
		if summary.CampaignID == "campaign-doomed" {
			t.Errorf("ListCampaignSummaries() still includes campaign-doomed: %+v", summary)
		}
	}

	// The survivor campaign's own rows must all still be there.
	if _, err := s.GetCharacter(ctx, "char-2"); err != nil {
		t.Errorf("GetCharacter(char-2) error = %v, want the survivor's character to still exist", err)
	}
	if events, _, err := s.ListEvents(ctx, "campaign-survivor", store.ListEventsOptions{}); err != nil || len(events) != 1 {
		t.Errorf("ListEvents(campaign-survivor) = %v, %v, want 1 event untouched", events, err)
	}
	if _, ok, err := s.GetCampaignSettings(ctx, "campaign-survivor"); err != nil || !ok {
		t.Errorf("GetCampaignSettings(campaign-survivor) ok = %v, err = %v, want ok=true", ok, err)
	}
}

func TestSQLiteEventStore_DeleteCampaign_MissingCampaignID_ReturnsError(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteCampaign(context.Background(), "")
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("DeleteCampaign() error = %v, want ErrCampaignIDRequired", err)
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
