// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package main

import (
	"archive/zip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packLibraryArchivePath is the committed pack-library zip the homepage
// links to and Master's admin panel downloads.
var packLibraryArchivePath = filepath.Join("web", "downloads", "campaign-pack-library.zip")

func TestPackLibraryArchive_PresentAndShapedLikeAPackLibrary(t *testing.T) {
	info, err := os.Stat(packLibraryArchivePath)
	if err != nil {
		t.Fatalf("pack library archive missing (%s): %v", packLibraryArchivePath, err)
	}
	if info.Size() == 0 {
		t.Fatal("pack library archive is empty")
	}

	zr, err := zip.OpenReader(packLibraryArchivePath)
	if err != nil {
		t.Fatalf("pack library archive is not a valid zip: %v", err)
	}
	defer zr.Close()

	packsWithCampaign := map[string]bool{}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Errorf("archive entry has an unsafe path: %q", f.Name)
		}
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) == 2 && parts[1] == "campaign.md" {
			packsWithCampaign[parts[0]] = true
		}
	}
	if len(packsWithCampaign) == 0 {
		t.Error("archive contains no <slug>/campaign.md entries — not a pack library")
	}
}

func TestStaticFileServer_ServesThePackLibraryArchive(t *testing.T) {
	if _, err := os.Stat(packLibraryArchivePath); err != nil {
		t.Skipf("archive not built yet: %v", err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir("web")))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/downloads/campaign-pack-library.zip")
	if err != nil {
		t.Fatalf("GET archive: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	if n == 0 {
		t.Error("served archive body was empty")
	}
}
