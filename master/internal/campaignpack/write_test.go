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

func TestAddFilesAndValidate_MergesCleanlyIntoExistingPack(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	err = campaignpack.AddFilesAndValidate(dir, []campaignpack.GeneratedFile{
		{Path: "locations/lighthouse-lamp-room.md", Content: "---\nid: lighthouse-lamp-room\nchapter: chapter-2\n---\nThe lamp room.\n"},
		{Path: "encounters/the-second-thing.md", Content: "---\nid: the-second-thing\nchapter: chapter-2\n---\nSomething else.\n"},
	})
	if err != nil {
		t.Fatalf("AddFilesAndValidate() error = %v", err)
	}

	pack, err := campaignpack.LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v", err)
	}
	if pack.ID != "haunted-lighthouse" {
		t.Errorf("pack.ID = %q, want haunted-lighthouse (pre-existing content should survive)", pack.ID)
	}
	if len(pack.Locations) != 2 {
		t.Errorf("len(Locations) = %d, want 2 (1 pre-existing + 1 new)", len(pack.Locations))
	}
	if len(pack.Encounters) != 2 {
		t.Errorf("len(Encounters) = %d, want 2 (1 pre-existing + 1 new)", len(pack.Encounters))
	}
}

func TestAddFilesAndValidate_CampaignMD_Rejected(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	err = campaignpack.AddFilesAndValidate(dir, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: "---\nid: replaced\n---\nOverwritten.\n"},
	})
	if err == nil {
		t.Fatal("AddFilesAndValidate() error = nil, want an error (campaign.md already exists)")
	}

	pack, loadErr := campaignpack.LoadPack(dir)
	if loadErr != nil {
		t.Fatalf("LoadPack() error = %v after a rejected add", loadErr)
	}
	if pack.ID != "haunted-lighthouse" {
		t.Errorf("pack.ID = %q, want haunted-lighthouse (campaign.md must be untouched)", pack.ID)
	}
}

func TestAddFilesAndValidate_DisallowedPath_RejectedWithoutTouchingExisting(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	err = campaignpack.AddFilesAndValidate(dir, []campaignpack.GeneratedFile{
		{Path: "../../etc/passwd", Content: "malicious"},
	})
	if err == nil {
		t.Fatal("AddFilesAndValidate() error = nil, want an error (disallowed path)")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "..", "..", "etc", "passwd")); !os.IsNotExist(statErr) {
		t.Error("disallowed path was written despite rejection")
	}
	if _, loadErr := campaignpack.LoadPack(dir); loadErr != nil {
		t.Errorf("LoadPack() error = %v after a rejected add, want the existing pack untouched", loadErr)
	}
}

func TestAddFilesAndValidate_ValidationFailure_RemovesOnlyNewlyAddedFiles(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	err = campaignpack.AddFilesAndValidate(dir, []campaignpack.GeneratedFile{
		// No closing "---" — malformed front matter, fails to parse.
		{Path: "locations/broken.md", Content: "---\nid: broken\nThis never closes.\n"},
	})
	if err == nil {
		t.Fatal("AddFilesAndValidate() error = nil, want an error (malformed front matter)")
	}

	if _, statErr := os.Stat(filepath.Join(dir, "locations", "broken.md")); !os.IsNotExist(statErr) {
		t.Error("the newly-added broken file still exists, want it removed on failure")
	}
	pack, loadErr := campaignpack.LoadPack(dir)
	if loadErr != nil {
		t.Fatalf("LoadPack() error = %v after a rolled-back add, want the pre-existing pack to still load", loadErr)
	}
	if len(pack.Locations) != 1 {
		t.Errorf("len(Locations) = %d, want 1 (only the pre-existing location, untouched)", len(pack.Locations))
	}
}

func TestAddFilesAndValidate_OverwritingExistingFile_ValidationFailure_RestoresOriginalContent(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	// Overwrites the pre-existing npcs/keeper-mara.md, then fails
	// validation via a second, unrelated broken file — the overwritten
	// file must be restored to its original content, not just left
	// overwritten or deleted.
	err = campaignpack.AddFilesAndValidate(dir, []campaignpack.GeneratedFile{
		{Path: "npcs/keeper-mara.md", Content: "---\nid: keeper-mara-REPLACED\n---\nOverwritten content.\n"},
		{Path: "locations/broken.md", Content: "---\nid: broken\nThis never closes.\n"},
	})
	if err == nil {
		t.Fatal("AddFilesAndValidate() error = nil, want an error (malformed front matter)")
	}

	restored, readErr := os.ReadFile(filepath.Join(dir, "npcs", "keeper-mara.md"))
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(restored) != "---\nid: keeper-mara\n---\nThe keeper.\n" {
		t.Errorf("npcs/keeper-mara.md content = %q, want its original content restored", restored)
	}
}

func TestAddFilesAndValidate_NoFiles_NoError(t *testing.T) {
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "haunted-lighthouse", wellFormedGeneratedFiles())
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}

	if err := campaignpack.AddFilesAndValidate(dir, nil); err != nil {
		t.Errorf("AddFilesAndValidate() error = %v, want nil for an empty file list", err)
	}
}
