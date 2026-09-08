// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
)

// zipFrom builds an in-memory zip from a name->content map and returns it
// in the (io.ReaderAt, int64) shape campaignpack.InstallLibrary expects.
func zipFrom(t *testing.T, files map[string]string) (*bytes.Reader, int64) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	b := buf.Bytes()
	return bytes.NewReader(b), int64(len(b))
}

// validPackFiles returns a minimal well-formed pack's files, keyed by
// their archive path (each under slug/).
func validPackFiles(slug string) map[string]string {
	return map[string]string{
		slug + "/campaign.md":          "---\nid: " + slug + "\ntitle: Test Pack\nlevel_range: \"1-3\"\npvp_policy: pve_only\n---\nBody.\n",
		slug + "/locations/a-place.md": "---\nid: a-place\n---\nA place.\n",
		slug + "/npcs/a-person.md":     "---\nid: a-person\n---\nA person.\n",
	}
}

func mergeFiles(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func TestInstallLibrary_ValidArchive_InstallsEveryPackSortedBySlug(t *testing.T) {
	root := t.TempDir()
	r, size := zipFrom(t, mergeFiles(validPackFiles("beta-pack"), validPackFiles("alpha-pack")))

	results, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{})
	if err != nil {
		t.Fatalf("InstallLibrary() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Slug != "alpha-pack" || results[1].Slug != "beta-pack" {
		t.Errorf("results not sorted by slug: %q, %q", results[0].Slug, results[1].Slug)
	}
	for _, res := range results {
		if res.Status != campaignpack.InstallStatusInstalled {
			t.Errorf("%s: status = %q (%s), want installed", res.Slug, res.Status, res.Detail)
		}
		if _, err := campaignpack.LoadPack(filepath.Join(root, res.Slug)); err != nil {
			t.Errorf("LoadPack(%s) error = %v", res.Slug, err)
		}
	}
}

func TestInstallLibrary_ExistingSlug_SkippedWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "alpha-pack")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "campaign.md")
	if err := os.WriteFile(sentinel, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, size := zipFrom(t, validPackFiles("alpha-pack"))
	results, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{})
	if err != nil {
		t.Fatalf("InstallLibrary() error = %v", err)
	}
	if len(results) != 1 || results[0].Status != campaignpack.InstallStatusSkippedExists {
		t.Fatalf("results = %+v, want one skipped_exists", results)
	}
	got, _ := os.ReadFile(sentinel)
	if string(got) != "ORIGINAL" {
		t.Errorf("existing pack was modified: campaign.md = %q", got)
	}
}

func TestInstallLibrary_ExistingSlug_ReplacedWithOverwrite(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "alpha-pack")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "stale.md"), []byte("STALE"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, size := zipFrom(t, validPackFiles("alpha-pack"))
	results, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("InstallLibrary() error = %v", err)
	}
	if len(results) != 1 || results[0].Status != campaignpack.InstallStatusInstalled {
		t.Fatalf("results = %+v, want one installed", results)
	}
	if _, err := os.Stat(filepath.Join(existing, "stale.md")); !os.IsNotExist(err) {
		t.Errorf("stale file survived overwrite: stat err = %v", err)
	}
	if _, err := campaignpack.LoadPack(existing); err != nil {
		t.Errorf("LoadPack after overwrite error = %v", err)
	}
}

func TestInstallLibrary_StructurallyUnsafeArchive_RejectedWholesale(t *testing.T) {
	cases := map[string]map[string]string{
		"parent traversal":        {"../evil.md": "x"},
		"absolute path":           {"/etc/passwd": "x"},
		"embedded traversal":      {"alpha/../../beta/campaign.md": "x"},
		"dot segment":             {"alpha/./campaign.md": "x"},
		"nested traversal in rel": {"alpha-pack/locations/../secret.md": "x"},
		"disallowed extension":    {"alpha-pack/notes.txt": "x"},
		"too-deep pack file":      {"alpha-pack/locations/sub/deep.md": "x"},
		"file at archive root":    {"campaign.md": "x"},
		"non-canonical slug":      {"Alpha-Pack/campaign.md": "x"},
		"backslash path":          {"alpha-pack\\campaign.md": "x"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			r, size := zipFrom(t, files)
			if _, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{}); err == nil {
				t.Fatal("InstallLibrary() error = nil, want a rejection")
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Errorf("destRoot not left empty: %v", entries)
			}
		})
	}
}

func TestInstallLibrary_OnePackFailsToParse_OthersStillInstalled(t *testing.T) {
	root := t.TempDir()
	broken := map[string]string{
		"broken-pack/campaign.md": "---\ntitle: No ID Here\n---\nBody.\n",
	}
	r, size := zipFrom(t, mergeFiles(validPackFiles("good-pack"), broken))

	results, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{})
	if err != nil {
		t.Fatalf("InstallLibrary() error = %v", err)
	}
	byslug := map[string]campaignpack.InstallResult{}
	for _, res := range results {
		byslug[res.Slug] = res
	}
	if byslug["broken-pack"].Status != campaignpack.InstallStatusFailed {
		t.Errorf("broken-pack status = %q, want failed", byslug["broken-pack"].Status)
	}
	if byslug["broken-pack"].Detail == "" {
		t.Error("broken-pack Detail is empty, want a parse error")
	}
	if _, err := os.Stat(filepath.Join(root, "broken-pack")); !os.IsNotExist(err) {
		t.Errorf("broken-pack directory should not exist: stat err = %v", err)
	}
	if byslug["good-pack"].Status != campaignpack.InstallStatusInstalled {
		t.Errorf("good-pack status = %q, want installed", byslug["good-pack"].Status)
	}
}

func TestInstallLibrary_NoStagingLeftBehind(t *testing.T) {
	root := t.TempDir()
	r, size := zipFrom(t, validPackFiles("alpha-pack"))
	if _, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{}); err != nil {
		t.Fatalf("InstallLibrary() error = %v", err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install-") {
			t.Errorf("staging directory left behind: %s", e.Name())
		}
	}
}

func TestInstallLibrary_OversizedEntry_Rejected(t *testing.T) {
	root := t.TempDir()
	files := validPackFiles("alpha-pack")
	files["alpha-pack/locations/huge.md"] = "---\nid: huge\n---\n" + strings.Repeat("x", 1<<20+1)
	r, size := zipFrom(t, files)
	if _, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{}); err == nil {
		t.Fatal("InstallLibrary() error = nil, want an over-size rejection")
	}
}

func TestInstallLibrary_TooManyFiles_Rejected(t *testing.T) {
	root := t.TempDir()
	files := validPackFiles("alpha-pack")
	for i := 0; i < 5001; i++ {
		files[fmt.Sprintf("alpha-pack/npcs/n%d.md", i)] = "---\nid: n" + fmt.Sprint(i) + "\n---\nx\n"
	}
	r, size := zipFrom(t, files)
	if _, err := campaignpack.InstallLibrary(r, size, root, campaignpack.InstallOptions{}); err == nil {
		t.Fatal("InstallLibrary() error = nil, want a file-count rejection")
	}
}

func TestInstallLibrary_RejectsBadInput(t *testing.T) {
	valid := func() (*bytes.Reader, int64) { return zipFrom(t, validPackFiles("alpha-pack")) }

	t.Run("empty dest root", func(t *testing.T) {
		r, size := valid()
		if _, err := campaignpack.InstallLibrary(r, size, "", campaignpack.InstallOptions{}); err == nil {
			t.Fatal("error = nil, want rejection")
		}
	})
	t.Run("zero size", func(t *testing.T) {
		if _, err := campaignpack.InstallLibrary(bytes.NewReader(nil), 0, t.TempDir(), campaignpack.InstallOptions{}); err == nil {
			t.Fatal("error = nil, want rejection")
		}
	})
	t.Run("size over archive limit", func(t *testing.T) {
		r, _ := valid()
		if _, err := campaignpack.InstallLibrary(r, 33<<20, t.TempDir(), campaignpack.InstallOptions{}); err == nil {
			t.Fatal("error = nil, want rejection")
		}
	})
	t.Run("not a zip", func(t *testing.T) {
		b := []byte("this is definitely not a zip archive")
		if _, err := campaignpack.InstallLibrary(bytes.NewReader(b), int64(len(b)), t.TempDir(), campaignpack.InstallOptions{}); err == nil {
			t.Fatal("error = nil, want rejection")
		}
	})
	t.Run("empty archive", func(t *testing.T) {
		r, size := zipFrom(t, map[string]string{})
		if _, err := campaignpack.InstallLibrary(r, size, t.TempDir(), campaignpack.InstallOptions{}); err == nil {
			t.Fatal("error = nil, want ErrEmptyLibraryArchive")
		}
	})
}

func TestInstallStatus_IsValid(t *testing.T) {
	cases := []struct {
		status campaignpack.InstallStatus
		want   bool
	}{
		{campaignpack.InstallStatusUnspecified, false},
		{campaignpack.InstallStatusInstalled, true},
		{campaignpack.InstallStatusSkippedExists, true},
		{campaignpack.InstallStatusFailed, true},
		{campaignpack.InstallStatus("bogus"), false},
	}
	for _, c := range cases {
		if got := c.status.IsValid(); got != c.want {
			t.Errorf("InstallStatus(%q).IsValid() = %v, want %v", c.status, got, c.want)
		}
	}
}
