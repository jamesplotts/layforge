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
// links to and Master's admin panel downloads; packTemplateArchivePath
// is the single-pack authoring template zip the homepage also links.
var (
	packLibraryArchivePath  = filepath.Join("web", "downloads", "campaign-pack-library.zip")
	packTemplateArchivePath = filepath.Join("web", "downloads", "campaign-pack-template.zip")
)

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

func TestPackTemplateArchive_PresentAndValid(t *testing.T) {
	info, err := os.Stat(packTemplateArchivePath)
	if err != nil {
		t.Fatalf("template archive missing (%s): %v", packTemplateArchivePath, err)
	}
	if info.Size() == 0 {
		t.Fatal("template archive is empty")
	}
	zr, err := zip.OpenReader(packTemplateArchivePath)
	if err != nil {
		t.Fatalf("template archive is not a valid zip: %v", err)
	}
	defer zr.Close()

	var sawCampaign bool
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Errorf("template archive entry has an unsafe path: %q", f.Name)
		}
		if strings.HasSuffix(f.Name, "/campaign.md") {
			sawCampaign = true
		}
	}
	if !sawCampaign {
		t.Error("template archive has no campaign.md")
	}
}

func TestStaticFileServer_ServesTheArchives(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("web")))
	defer srv.Close()

	for _, name := range []string{"campaign-pack-library.zip", "campaign-pack-template.zip"} {
		if _, err := os.Stat(filepath.Join("web", "downloads", name)); err != nil {
			t.Skipf("%s not built yet: %v", name, err)
		}
		resp, err := http.Get(srv.URL + "/downloads/" + name)
		if err != nil {
			t.Fatalf("GET %s: %v", name, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("%s status = %d, want 200", name, resp.StatusCode)
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if n == 0 {
			t.Errorf("%s served body was empty", name)
		}
	}
}
