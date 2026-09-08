// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
)

// sableRavineDir is the real, committed example pack (campaign-packs/
// sable-ravine/ at the repo root) — used as a real fixture rather than
// a hand-built one, so this test proves the loader actually parses the
// content this project ships, not just a shape convenient for testing.
const sableRavineDir = "../../../campaign-packs/sable-ravine"

// templateDir is the committed authoring template (campaign-packs/
// TEMPLATE/). It must always load, since it's what a pack author copies
// to start from — see docs/authoring-campaign-packs.md.
const templateDir = "../../../campaign-packs/TEMPLATE"

func TestLoadPack_AuthoringTemplate_Parses(t *testing.T) {
	pack, err := campaignpack.LoadPack(templateDir)
	if err != nil {
		t.Fatalf("LoadPack(%q) error = %v", templateDir, err)
	}
	if pack.ID == "" {
		t.Error("template pack has an empty ID")
	}
	if len(pack.Locations) == 0 || len(pack.NPCs) == 0 || len(pack.Encounters) == 0 {
		t.Errorf("template should exercise every section: %d locations, %d npcs, %d encounters",
			len(pack.Locations), len(pack.NPCs), len(pack.Encounters))
	}
}

func TestLoadPack_RealSableRavineFixture_ParsesAllContent(t *testing.T) {
	pack, err := campaignpack.LoadPack(sableRavineDir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v", err)
	}

	if pack.ID != "sable-ravine" {
		t.Errorf("ID = %q, want %q", pack.ID, "sable-ravine")
	}
	if pack.Title != "The Sable Ravine" {
		t.Errorf("Title = %q, want %q", pack.Title, "The Sable Ravine")
	}
	if pack.LevelRange != "1-3" {
		t.Errorf("LevelRange = %q, want %q", pack.LevelRange, "1-3")
	}
	if pack.PvPPolicy != "pve_only" {
		t.Errorf("PvPPolicy = %q, want %q", pack.PvPPolicy, "pve_only")
	}
	if pack.MaturityTier != "standard" {
		t.Errorf("MaturityTier = %q, want %q", pack.MaturityTier, "standard")
	}
	if pack.ImageMaturityTier != "family_friendly" {
		t.Errorf("ImageMaturityTier = %q, want %q", pack.ImageMaturityTier, "family_friendly")
	}
	if pack.SharedKnowledge != "strict" {
		t.Errorf("SharedKnowledge = %q, want %q", pack.SharedKnowledge, "strict")
	}
	if len(pack.Lines) != 1 {
		t.Errorf("len(Lines) = %d, want 1", len(pack.Lines))
	}
	if len(pack.Veils) != 1 {
		t.Errorf("len(Veils) = %d, want 1", len(pack.Veils))
	}
	if len(pack.ContentWarnings) == 0 {
		t.Error("ContentWarnings is empty, want real content")
	}
	if pack.Overview == "" {
		t.Error("Overview (campaign.md body) is empty")
	}

	if len(pack.Locations) != 6 {
		t.Fatalf("len(Locations) = %d, want 6", len(pack.Locations))
	}
	var ravine *campaignpack.Location
	for i := range pack.Locations {
		if pack.Locations[i].ID == "sable-ravine" {
			ravine = &pack.Locations[i]
		}
	}
	if ravine == nil {
		t.Fatal("no location with ID \"sable-ravine\" found")
	}
	wantConnections := []string{"old-road", "goblin-camp", "kobold-warren", "ruined-shrine"}
	if len(ravine.Connections) != len(wantConnections) {
		t.Fatalf("sable-ravine Connections = %v, want %v", ravine.Connections, wantConnections)
	}
	for i, want := range wantConnections {
		if ravine.Connections[i] != want {
			t.Errorf("sable-ravine Connections[%d] = %q, want %q", i, ravine.Connections[i], want)
		}
	}
	if ravine.Body == "" {
		t.Error("sable-ravine location Body is empty")
	}

	if len(pack.NPCs) != 4 {
		t.Fatalf("len(NPCs) = %d, want 4", len(pack.NPCs))
	}
	var vashti *campaignpack.NPC
	for i := range pack.NPCs {
		if pack.NPCs[i].ID == "captain-orlen-vashti" {
			vashti = &pack.NPCs[i]
		}
	}
	if vashti == nil {
		t.Fatal("no NPC with ID \"captain-orlen-vashti\" found")
	}
	if vashti.Location != "keep-stonewatch" {
		t.Errorf("vashti Location = %q, want %q", vashti.Location, "keep-stonewatch")
	}
	if vashti.StatBlockRef != "SRD Veteran" {
		t.Errorf("vashti StatBlockRef = %q, want %q", vashti.StatBlockRef, "SRD Veteran")
	}
	if vashti.Voice == "" {
		t.Error("vashti Voice is empty")
	}
	if vashti.Body == "" {
		t.Error("vashti Body is empty")
	}

	if len(pack.Encounters) != 3 {
		t.Fatalf("len(Encounters) = %d, want 3", len(pack.Encounters))
	}
	var ambush *campaignpack.Encounter
	for i := range pack.Encounters {
		if pack.Encounters[i].ID == "ambush-on-the-old-road" {
			ambush = &pack.Encounters[i]
		}
	}
	if ambush == nil {
		t.Fatal("no encounter with ID \"ambush-on-the-old-road\" found")
	}
	if ambush.Location != "old-road" {
		t.Errorf("ambush Location = %q, want %q", ambush.Location, "old-road")
	}
	if len(ambush.Involves) != 1 || ambush.Involves[0] != "goblin-chief-skreel" {
		t.Errorf("ambush Involves = %v, want [goblin-chief-skreel]", ambush.Involves)
	}
}

// TestLoadPack_RealSableRavineFixture_HasNoChaptersOrSideQuests is the
// explicit backward-compatibility regression check for adding
// chapters/side quests to the schema: a pack that predates the concept
// entirely must still load with zero Chapters and no file carrying a
// Chapter/SideQuest tag, not an error and not some default value.
func TestLoadPack_RealSableRavineFixture_HasNoChaptersOrSideQuests(t *testing.T) {
	pack, err := campaignpack.LoadPack(sableRavineDir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v", err)
	}
	if len(pack.Chapters) != 0 {
		t.Errorf("Chapters = %v, want empty for a pack with no chapters: front matter", pack.Chapters)
	}
	for _, loc := range pack.Locations {
		if loc.Chapter != "" || loc.SideQuest != "" {
			t.Errorf("location %q: Chapter = %q, SideQuest = %q, want both empty", loc.ID, loc.Chapter, loc.SideQuest)
		}
	}
	for _, npc := range pack.NPCs {
		if npc.Chapter != "" || npc.SideQuest != "" {
			t.Errorf("npc %q: Chapter = %q, SideQuest = %q, want both empty", npc.ID, npc.Chapter, npc.SideQuest)
		}
	}
	for _, enc := range pack.Encounters {
		if enc.Chapter != "" || enc.SideQuest != "" {
			t.Errorf("encounter %q: Chapter = %q, SideQuest = %q, want both empty", enc.ID, enc.Chapter, enc.SideQuest)
		}
		if enc.MinPlayers != 0 || enc.MaxPlayers != 0 {
			t.Errorf("encounter %q: MinPlayers = %d, MaxPlayers = %d, want both 0", enc.ID, enc.MinPlayers, enc.MaxPlayers)
		}
	}
}

func TestLoadPack_ChaptersAndSideQuests_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "campaign.md"), `---
id: test
title: Test
chapters:
  - id: chapter-1
    title: "Into the Wilds"
    level_range: "1-2"
    summary: "The party sets out."
  - id: chapter-2
    title: "The Deeper Dark"
    level_range: "2-3"
    summary: "The party descends."
---
Overview.
`)
	if err := os.Mkdir(filepath.Join(dir, "locations"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "locations", "camp.md"), "---\nid: camp\nchapter: chapter-1\n---\nA camp.\n")
	if err := os.Mkdir(filepath.Join(dir, "npcs"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "npcs", "peddler.md"), "---\nid: peddler\nside_quest: sq-lost-cart\n---\nA peddler.\n")
	if err := os.Mkdir(filepath.Join(dir, "encounters"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "encounters", "lost-cart.md"), "---\nid: lost-cart\nside_quest: sq-lost-cart\nmin_players: 1\nmax_players: 3\n---\nA lost cart.\n")

	pack, err := campaignpack.LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v", err)
	}

	if len(pack.Chapters) != 2 {
		t.Fatalf("len(Chapters) = %d, want 2", len(pack.Chapters))
	}
	if pack.Chapters[0] != (campaignpack.Chapter{ID: "chapter-1", Title: "Into the Wilds", LevelRange: "1-2", Summary: "The party sets out."}) {
		t.Errorf("Chapters[0] = %+v, want chapter-1", pack.Chapters[0])
	}
	if pack.Chapters[1].ID != "chapter-2" {
		t.Errorf("Chapters[1].ID = %q, want chapter-2", pack.Chapters[1].ID)
	}

	if len(pack.Locations) != 1 || pack.Locations[0].Chapter != "chapter-1" {
		t.Fatalf("Locations = %+v, want one location tagged chapter-1", pack.Locations)
	}
	if len(pack.NPCs) != 1 || pack.NPCs[0].SideQuest != "sq-lost-cart" {
		t.Fatalf("NPCs = %+v, want one npc tagged side_quest sq-lost-cart", pack.NPCs)
	}
	if len(pack.Encounters) != 1 {
		t.Fatalf("len(Encounters) = %d, want 1", len(pack.Encounters))
	}
	enc := pack.Encounters[0]
	if enc.SideQuest != "sq-lost-cart" {
		t.Errorf("Encounters[0].SideQuest = %q, want sq-lost-cart", enc.SideQuest)
	}
	if enc.MinPlayers != 1 || enc.MaxPlayers != 3 {
		t.Errorf("Encounters[0].MinPlayers/MaxPlayers = %d/%d, want 1/3", enc.MinPlayers, enc.MaxPlayers)
	}
}

func TestLoadPack_MissingDirectory_ReturnsError(t *testing.T) {
	_, err := campaignpack.LoadPack("/nonexistent/campaign-pack-directory")
	if err == nil {
		t.Fatal("LoadPack() error = nil, want an error")
	}
}

func TestLoadPack_MissingCampaignMd_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := campaignpack.LoadPack(dir)
	if err == nil {
		t.Fatal("LoadPack() error = nil, want an error for a directory with no campaign.md")
	}
}

func TestLoadPack_MalformedLocationFrontMatter_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "campaign.md"), "---\nid: test\ntitle: Test\n---\nOverview.\n")
	if err := os.Mkdir(filepath.Join(dir, "locations"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	// No closing "---" delimiter.
	writeFile(t, filepath.Join(dir, "locations", "broken.md"), "---\nid: broken\nThis never closes the front matter.\n")

	_, err := campaignpack.LoadPack(dir)
	if err == nil {
		t.Fatal("LoadPack() error = nil, want an error for malformed front matter")
	}
}

func TestLoadPack_NoNPCsOrEncountersDirectories_ReturnsEmptySlicesNotError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "campaign.md"), "---\nid: test\ntitle: Test\n---\nOverview.\n")

	pack, err := campaignpack.LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v, want nil (npcs/encounters directories are optional)", err)
	}
	if len(pack.NPCs) != 0 {
		t.Errorf("NPCs = %v, want empty", pack.NPCs)
	}
	if len(pack.Encounters) != 0 {
		t.Errorf("Encounters = %v, want empty", pack.Encounters)
	}
	if len(pack.Locations) != 0 {
		t.Errorf("Locations = %v, want empty", pack.Locations)
	}
}

func TestLoadPack_LocationMissingID_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "campaign.md"), "---\nid: test\ntitle: Test\n---\nOverview.\n")
	if err := os.Mkdir(filepath.Join(dir, "locations"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "locations", "no-id.md"), "---\nconnections: []\n---\nA place with no id.\n")

	_, err := campaignpack.LoadPack(dir)
	if err == nil {
		t.Fatal("LoadPack() error = nil, want an error for a location with no id")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
