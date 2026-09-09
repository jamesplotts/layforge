// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/session"
)

// newTestServerWithPacksDir is newTestServer plus a real, empty
// campaign-packs directory — for the "Install the campaign pack library"
// endpoint, which rejects with a "not configured" error against
// newTestServer's own empty default. It returns the packs dir so a test
// can assert what landed on disk.
func newTestServerWithPacksDir(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestStore(t)
	packsDir := t.TempDir()
	srv := admin.New(logger, s, s, s, s, "", "127.0.0.1:8090", nil, nil, nil, "", packsDir, "", session.NewHub())
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, packsDir
}

// libraryArchiveServer serves a zip built from the given name->content
// map at /downloads/campaign-pack-library.zip, 404ing everything else.
func libraryArchiveServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	body := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/downloads/campaign-pack-library.zip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func validPackArchiveFiles(slug string) map[string]string {
	return map[string]string{
		slug + "/campaign.md":          "---\nid: " + slug + "\ntitle: Test Pack\nlevel_range: \"1-3\"\npvp_policy: pve_only\n---\nBody.\n",
		slug + "/locations/a-place.md": "---\nid: a-place\n---\nA place.\n",
	}
}

func TestHandleInstallCampaignPackLibrary_NoPacksDir_400(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library", map[string]any{}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleInstallCampaignPackLibrary_CrossOriginRequest_Rejected(t *testing.T) {
	httpSrv, _ := newTestServerWithPacksDir(t)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestHandleInstallCampaignPackLibrary_HappyPath_InstallsFromArchiveServer(t *testing.T) {
	httpSrv, packsDir := newTestServerWithPacksDir(t)
	archiveSrv := libraryArchiveServer(t, mergeArchiveFiles(
		validPackArchiveFiles("alpha-pack"),
		validPackArchiveFiles("beta-pack"),
	))

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{"url": archiveSrv.URL + "/downloads/campaign-pack-library.zip"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}

	var got struct {
		Source         string `json:"source"`
		CampaignsAdded int    `json:"campaigns_added"`
		Results        []struct {
			Slug, Status, Detail, Campaign string
			CampaignAdded                  bool `json:"campaign_added"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results = %+v, want 2", got.Results)
	}
	if got.CampaignsAdded != 2 {
		t.Errorf("campaigns_added = %d, want 2", got.CampaignsAdded)
	}
	for _, res := range got.Results {
		if res.Status != "installed" {
			t.Errorf("%s: status = %q (%s), want installed", res.Slug, res.Status, res.Detail)
		}
		if _, err := os.Stat(filepath.Join(packsDir, res.Slug, "campaign.md")); err != nil {
			t.Errorf("%s not on disk: %v", res.Slug, err)
		}
		if res.Campaign != res.Slug || !res.CampaignAdded {
			t.Errorf("%s: campaign=%q campaign_added=%v, want a new campaign under the pack id", res.Slug, res.Campaign, res.CampaignAdded)
		}
	}

	// The new campaigns are now selectable via GET /api/campaigns, each
	// with the library pack bound.
	listResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	var list struct {
		Campaigns []struct {
			CampaignID  string `json:"campaign_id"`
			DisplayName string `json:"display_name"`
		} `json:"campaigns"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decoding campaign list: %v", err)
	}
	byID := map[string]string{}
	for _, c := range list.Campaigns {
		byID[c.CampaignID] = c.DisplayName
	}
	for _, slug := range []string{"alpha-pack", "beta-pack"} {
		if byID[slug] != "Test Pack" {
			t.Errorf("campaign %q display name = %q, want %q", slug, byID[slug], "Test Pack")
		}
		packResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/"+slug+"/pack", nil, "")
		var bound struct {
			PackDir string `json:"pack_dir"`
			PackID  string `json:"pack_id"`
		}
		_ = json.NewDecoder(packResp.Body).Decode(&bound)
		if bound.PackID != slug || bound.PackDir == "" {
			t.Errorf("campaign %q pack binding = %+v, want the library pack bound", slug, bound)
		}
	}

	// A second run skips both packs, and adds no campaigns (they exist).
	resp2 := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{"url": archiveSrv.URL + "/downloads/campaign-pack-library.zip"}, "")
	var got2 struct {
		CampaignsAdded int `json:"campaigns_added"`
		Results        []struct {
			Status        string
			CampaignAdded bool `json:"campaign_added"`
		} `json:"results"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&got2)
	if got2.CampaignsAdded != 0 {
		t.Errorf("second run campaigns_added = %d, want 0", got2.CampaignsAdded)
	}
	for _, res := range got2.Results {
		if res.Status != "skipped_exists" || res.CampaignAdded {
			t.Errorf("second run: status=%q campaign_added=%v, want skipped_exists / false", res.Status, res.CampaignAdded)
		}
	}
}

func TestHandleInstallCampaignPackLibrary_DoesNotClobberAnExistingBinding(t *testing.T) {
	httpSrv, packsDir := newTestServerWithPacksDir(t)

	// Host already has a campaign "alpha-pack" bound to their own pack
	// dir before the library is installed.
	ownPack := filepath.Join(packsDir, "my-own")
	if err := os.MkdirAll(ownPack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownPack, "campaign.md"),
		[]byte("---\nid: alpha-pack\ntitle: My Homebrew\n---\nMine.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/alpha-pack/pack",
		map[string]any{"pack_dir": ownPack}, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("pre-bind status = %d", r.StatusCode)
	}

	archiveSrv := libraryArchiveServer(t, validPackArchiveFiles("alpha-pack"))
	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{"url": archiveSrv.URL + "/downloads/campaign-pack-library.zip"}, "")
	var got struct {
		CampaignsAdded int `json:"campaigns_added"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.CampaignsAdded != 0 {
		t.Errorf("campaigns_added = %d, want 0 (the id was already bound)", got.CampaignsAdded)
	}

	packResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/alpha-pack/pack", nil, "")
	var bound struct {
		PackDir string `json:"pack_dir"`
	}
	_ = json.NewDecoder(packResp.Body).Decode(&bound)
	if bound.PackDir != ownPack {
		t.Errorf("binding = %q, want the Host's own pack %q left untouched", bound.PackDir, ownPack)
	}
}

func TestHandleInstallCampaignPackLibrary_UpstreamError_502(t *testing.T) {
	httpSrv, _ := newTestServerWithPacksDir(t)
	archiveSrv := libraryArchiveServer(t, validPackArchiveFiles("alpha-pack"))

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{"url": archiveSrv.URL + "/downloads/nope.zip"}, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestHandleInstallCampaignPackLibrary_NonHTTPURL_400(t *testing.T) {
	httpSrv, _ := newTestServerWithPacksDir(t)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/install-library",
		map[string]any{"url": "file:///etc/passwd"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-http(s) URL", resp.StatusCode)
	}
}

func mergeArchiveFiles(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
