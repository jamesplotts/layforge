// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
)

func wellFormedGeneratedFiles() []campaignpack.GeneratedFile {
	return []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
		{Path: "locations/lighthouse-base.md", Content: "---\nid: lighthouse-base\n---\nThe base.\n"},
		{Path: "npcs/keeper-mara.md", Content: "---\nid: keeper-mara\n---\nThe keeper.\n"},
		{Path: "encounters/the-drowned-thing.md", Content: "---\nid: the-drowned-thing\n---\nSomething rises.\n"},
	}
}

func TestWriteAndValidate_WellFormedFiles_WritesAndLoadsSuccessfully(t *testing.T) {
	root := t.TempDir()

	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}
	if dir != filepath.Join(root, "haunted-lighthouse") {
		t.Errorf("dir = %q, want %q", dir, filepath.Join(root, "haunted-lighthouse"))
	}

	pack, err := campaignpack.LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack(%q) error = %v", dir, err)
	}
	if pack.ID != "haunted-lighthouse" {
		t.Errorf("pack.ID = %q, want haunted-lighthouse", pack.ID)
	}
}

func TestWriteAndValidate_InvalidPack_CleansUpAndReturnsError(t *testing.T) {
	root := t.TempDir()

	_, err := campaignpack.WriteAndValidate(root, "broken-pack", []campaignpack.GeneratedFile{
		{Path: "locations/somewhere.md", Content: "---\nid: somewhere\n---\nNo campaign.md at all.\n"},
	})
	if err == nil {
		t.Fatal("WriteAndValidate() error = nil, want an error (no campaign.md)")
	}

	if _, statErr := os.Stat(filepath.Join(root, "broken-pack")); !os.IsNotExist(statErr) {
		t.Errorf("directory still exists after a failed validation, want it cleaned up (stat err = %v)", statErr)
	}
}

func TestWriteAndValidate_DisallowedFilePath_RejectedBeforeWritingAnything(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name string
		path string
	}{
		{name: "AbsolutePath", path: "/etc/passwd"},
		{name: "PathTraversal", path: "../../etc/passwd"},
		{name: "UnexpectedSubdirectory", path: "scripts/evil.sh"},
		{name: "UnexpectedTopLevelFile", path: "README.md"},
		{name: "NestedTraversalWithinAllowedDir", path: "locations/../../../etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := "reject-" + tt.name
			_, err := campaignpack.WriteAndValidate(root, slug, []campaignpack.GeneratedFile{
				{Path: "campaign.md", Content: validCampaignMD()},
				{Path: tt.path, Content: "malicious"},
			})
			if err == nil {
				t.Fatalf("WriteAndValidate() error = nil for path %q, want rejected", tt.path)
			}
			// Nothing from this attempt should have been written anywhere,
			// including outside root — the real point of this test.
			if _, statErr := os.Stat(filepath.Join(root, slug)); !os.IsNotExist(statErr) {
				t.Errorf("directory %q exists after a rejected write, want it never created", slug)
			}
		})
	}
}

func TestWriteAndValidate_UnsafeSlug_Rejected(t *testing.T) {
	root := t.TempDir()

	_, err := campaignpack.WriteAndValidate(root, "../escape", wellFormedGeneratedFiles())
	if err == nil {
		t.Fatal("WriteAndValidate() error = nil, want an error (unsafe slug)")
	}
}
