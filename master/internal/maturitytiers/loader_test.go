// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package maturitytiers_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/maturitytiers"
)

// tiersDir is the real, committed example tier library (maturity-tiers/
// at the repo root) — used as a real fixture rather than a hand-built
// one, mirroring campaignpack's own loader_test.go.
const tiersDir = "../../../maturity-tiers"

func TestLoadTiers_RealFixtureDirectory_ParsesAllThreeTiers(t *testing.T) {
	tiers, err := maturitytiers.LoadTiers(tiersDir)
	if err != nil {
		t.Fatalf("LoadTiers() error = %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("len(tiers) = %d, want 3", len(tiers))
	}

	standard, ok := tiers["standard"]
	if !ok {
		t.Fatal("no tier with id \"standard\" found")
	}
	if standard.DisplayName != "Standard" {
		t.Errorf("standard.DisplayName = %q, want %q", standard.DisplayName, "Standard")
	}
	if standard.Rank != 1 {
		t.Errorf("standard.Rank = %d, want 1", standard.Rank)
	}
	if standard.Prompt == "" {
		t.Error("standard.Prompt is empty")
	}

	familyFriendly, ok := tiers["family_friendly"]
	if !ok {
		t.Fatal("no tier with id \"family_friendly\" found")
	}
	if familyFriendly.Rank != 0 {
		t.Errorf("family_friendly.Rank = %d, want 0", familyFriendly.Rank)
	}

	mature, ok := tiers["mature"]
	if !ok {
		t.Fatal("no tier with id \"mature\" found")
	}
	if mature.Rank != 2 {
		t.Errorf("mature.Rank = %d, want 2", mature.Rank)
	}

	// family_friendly (most restrictive) < standard < mature (least
	// restrictive) — the real ordering a rank-based sanity check would
	// rely on.
	if !(familyFriendly.Rank < standard.Rank && standard.Rank < mature.Rank) {
		t.Errorf("rank ordering wrong: family_friendly=%d standard=%d mature=%d, want strictly increasing",
			familyFriendly.Rank, standard.Rank, mature.Rank)
	}
}

func TestLoadTiers_MissingDirectory_ReturnsError(t *testing.T) {
	_, err := maturitytiers.LoadTiers("/nonexistent/maturity-tiers-directory")
	if err == nil {
		t.Fatal("LoadTiers() error = nil, want an error")
	}
}

func TestLoadTiers_MalformedFrontMatter_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.md"), "---\nid: broken\nThis never closes the front matter.\n")

	_, err := maturitytiers.LoadTiers(dir)
	if err == nil {
		t.Fatal("LoadTiers() error = nil, want an error for malformed front matter")
	}
}

func TestLoadTiers_MissingID_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "no-id.md"), "---\ndisplay_name: No ID\nrank: 1\n---\nBody.\n")

	_, err := maturitytiers.LoadTiers(dir)
	if err == nil {
		t.Fatal("LoadTiers() error = nil, want an error for a tier with no id")
	}
}

func TestLoadTiers_DuplicateID_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nid: dup\ndisplay_name: A\nrank: 0\n---\nBody A.\n")
	writeFile(t, filepath.Join(dir, "b.md"), "---\nid: dup\ndisplay_name: B\nrank: 1\n---\nBody B.\n")

	_, err := maturitytiers.LoadTiers(dir)
	if err == nil {
		t.Fatal("LoadTiers() error = nil, want an error for two files sharing an id")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
