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
