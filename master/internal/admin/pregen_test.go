// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestServer_PutThenListPregens_RoundTrips(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	url := httpSrv.URL + "/api/campaigns/campaign-1/pregens"

	putResp := doJSON(t, http.MethodPut, url, map[string]any{
		"id":             "bram-fighter",
		"name":           "Bram the Bold",
		"description":    "A stalwart level-1 fighter, ready to go.",
		"schema_version": "opencombatengine-v1",
		"character_json": map[string]any{"name": "Bram"},
	}, "")
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d, body = %s", putResp.StatusCode, body)
	}

	listResp := doJSON(t, http.MethodGet, url, nil, "")
	var got []struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		SchemaVersion string          `json:"schema_version"`
		CharacterJSON json.RawMessage `json:"character_json"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListPregens response = %+v, want exactly one pregen", got)
	}
	if got[0].ID != "bram-fighter" || got[0].Name != "Bram the Bold" || got[0].SchemaVersion != "opencombatengine-v1" {
		t.Errorf("got[0] = %+v, want the pregen just saved", got[0])
	}
}

func TestServer_ListPregens_NoneBound_ReturnsEmptyList(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/campaign-never-bound/pregens", nil, "")
	var got []any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPregens response = %v, want empty", got)
	}
}

func TestServer_PutPregen_MissingID_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/pregens", map[string]any{
		"name":           "Bram the Bold",
		"character_json": map[string]any{"name": "Bram"},
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a missing id", resp.StatusCode)
	}
}

func TestServer_PutPregen_MissingCharacterJSON_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/pregens", map[string]any{
		"id":   "bram-fighter",
		"name": "Bram the Bold",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing character_json", resp.StatusCode)
	}
}

func TestServer_DeletePregen_RemovesIt(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	url := httpSrv.URL + "/api/campaigns/campaign-1/pregens"

	if resp := doJSON(t, http.MethodPut, url, map[string]any{
		"id": "bram-fighter", "name": "Bram the Bold", "character_json": map[string]any{"name": "Bram"},
	}, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}

	delResp := doJSON(t, http.MethodDelete, url+"/bram-fighter", nil, "")
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", delResp.StatusCode)
	}

	listResp := doJSON(t, http.MethodGet, url, nil, "")
	var got []any
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPregens after delete = %v, want empty", got)
	}
}

func TestServer_PutPregen_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/pregens", map[string]any{
		"id": "bram-fighter", "name": "Bram the Bold", "character_json": map[string]any{"name": "Bram"},
	}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}
