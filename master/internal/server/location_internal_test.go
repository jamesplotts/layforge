// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// newLocationTestServer/bindTestPackWithChapterAndSideQuest build a
// minimal real *Server (package server, not server_test, since
// dmListLocations/dmListNPCs/dmListEncounters are unexported) backed by
// a real on-disk pack with one chapter-tagged location and one
// side_quest-tagged NPC/encounter pair — proving the new fields these
// three DM tools expose (chapter/side_quest/min_players/max_players)
// actually reach the JSON a real DM tool call would see, not just that
// campaignpack.LoadPack parses them (already covered in
// internal/campaignpack's own tests).
func newLocationTestServer(t *testing.T) (*Server, *store.SQLiteEventStore) {
	t.Helper()
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), campaignPack: st}, st
}

func bindTestPackWithChapterAndSideQuest(t *testing.T, st *store.SQLiteEventStore, campaignID string) {
	t.Helper()
	dir, err := campaignpack.WriteAndValidate(t.TempDir(), "test-pack", []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: "---\nid: test-pack\ntitle: Test\n---\nOverview.\n"},
		{Path: "locations/camp.md", Content: "---\nid: camp\nchapter: chapter-1\n---\nA camp.\n"},
		{Path: "npcs/peddler.md", Content: "---\nid: peddler\nside_quest: sq-lost-cart\n---\nA peddler.\n"},
		{Path: "encounters/lost-cart.md", Content: "---\nid: lost-cart\nside_quest: sq-lost-cart\nmin_players: 1\nmax_players: 3\n---\nA lost cart.\n"},
	})
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}
	if err := st.SaveCampaignPack(context.Background(), campaignID, dir, "test-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}
}

func TestDmListLocations_IncludesChapterAndSideQuestFields(t *testing.T) {
	srv, st := newLocationTestServer(t)
	bindTestPackWithChapterAndSideQuest(t, st, "campaign-1")

	resultJSON, ok, reason := srv.dmListLocations(context.Background(), "campaign-1")
	if !ok {
		t.Fatalf("dmListLocations() ok = false, reason = %q", reason)
	}
	var got struct {
		Locations []struct {
			ID        string `json:"id"`
			Chapter   string `json:"chapter"`
			SideQuest string `json:"side_quest"`
		} `json:"locations"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v (result: %s)", err, resultJSON)
	}
	if len(got.Locations) != 1 || got.Locations[0].Chapter != "chapter-1" {
		t.Errorf("Locations = %+v, want one location tagged chapter-1", got.Locations)
	}
}

func TestDmListNPCs_IncludesChapterAndSideQuestFields(t *testing.T) {
	srv, st := newLocationTestServer(t)
	bindTestPackWithChapterAndSideQuest(t, st, "campaign-1")

	resultJSON, ok, reason := srv.dmListNPCs(context.Background(), "campaign-1")
	if !ok {
		t.Fatalf("dmListNPCs() ok = false, reason = %q", reason)
	}
	var got struct {
		NPCs []struct {
			ID        string `json:"id"`
			SideQuest string `json:"side_quest"`
		} `json:"npcs"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v (result: %s)", err, resultJSON)
	}
	if len(got.NPCs) != 1 || got.NPCs[0].SideQuest != "sq-lost-cart" {
		t.Errorf("NPCs = %+v, want one npc tagged side_quest sq-lost-cart", got.NPCs)
	}
}

func TestDmListEncounters_IncludesChapterSideQuestAndPlayerCountFields(t *testing.T) {
	srv, st := newLocationTestServer(t)
	bindTestPackWithChapterAndSideQuest(t, st, "campaign-1")

	resultJSON, ok, reason := srv.dmListEncounters(context.Background(), "campaign-1")
	if !ok {
		t.Fatalf("dmListEncounters() ok = false, reason = %q", reason)
	}
	var got struct {
		Encounters []struct {
			ID         string `json:"id"`
			SideQuest  string `json:"side_quest"`
			MinPlayers int    `json:"min_players"`
			MaxPlayers int    `json:"max_players"`
		} `json:"encounters"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v (result: %s)", err, resultJSON)
	}
	if len(got.Encounters) != 1 {
		t.Fatalf("len(Encounters) = %d, want 1", len(got.Encounters))
	}
	enc := got.Encounters[0]
	if enc.SideQuest != "sq-lost-cart" || enc.MinPlayers != 1 || enc.MaxPlayers != 3 {
		t.Errorf("Encounters[0] = %+v, want side_quest sq-lost-cart, min 1, max 3", enc)
	}
}

// TestDmListLocations_NoChapterOrSideQuest_FieldsEmpty is the backward-
// compatibility check: a pack using neither concept (like sable-ravine)
// must report empty strings, not some other zero-value surprise, for
// every location.
func TestDmListLocations_NoChapterOrSideQuest_FieldsEmpty(t *testing.T) {
	srv, st := newLocationTestServer(t)
	dir, err := campaignpack.WriteAndValidate(t.TempDir(), "plain-pack", []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: "---\nid: plain-pack\ntitle: Plain\n---\nOverview.\n"},
		{Path: "locations/somewhere.md", Content: "---\nid: somewhere\n---\nSomewhere.\n"},
	})
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}
	if err := st.SaveCampaignPack(context.Background(), "campaign-1", dir, "plain-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	resultJSON, ok, reason := srv.dmListLocations(context.Background(), "campaign-1")
	if !ok {
		t.Fatalf("dmListLocations() ok = false, reason = %q", reason)
	}
	var got struct {
		Locations []struct {
			Chapter   string `json:"chapter"`
			SideQuest string `json:"side_quest"`
		} `json:"locations"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v (result: %s)", err, resultJSON)
	}
	if len(got.Locations) != 1 || got.Locations[0].Chapter != "" || got.Locations[0].SideQuest != "" {
		t.Errorf("Locations = %+v, want Chapter and SideQuest both empty", got.Locations)
	}
}
