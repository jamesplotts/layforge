// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeSession(t *testing.T, resp *http.Response) struct {
	ActiveCampaignID   string `json:"active_campaign_id"`
	ActiveCampaignName string `json:"active_campaign_name"`
	JoinLocked         bool   `json:"join_locked"`
} {
	t.Helper()
	var got struct {
		ActiveCampaignID   string `json:"active_campaign_id"`
		ActiveCampaignName string `json:"active_campaign_name"`
		JoinLocked         bool   `json:"join_locked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding session response: %v", err)
	}
	return got
}

func TestSession_Default_NoActiveCampaign(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	got := decodeSession(t, doJSON(t, http.MethodGet, httpSrv.URL+"/api/session", nil, ""))
	if got.ActiveCampaignID != "" || got.JoinLocked {
		t.Errorf("fresh session = %+v, want empty + unlocked", got)
	}
}

func TestSession_SetActiveCampaign_RoundTrips(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	if resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns",
		map[string]any{"campaign_id": "sunken-vault", "display_name": "The Sunken Vault"}, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("create campaign: status %d", resp.StatusCode)
	}

	setResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/session/active-campaign",
		map[string]any{"campaign_id": "sunken-vault"}, "")
	if setResp.StatusCode != http.StatusOK {
		t.Fatalf("set active campaign: status %d", setResp.StatusCode)
	}
	got := decodeSession(t, setResp)
	if got.ActiveCampaignID != "sunken-vault" || got.ActiveCampaignName != "The Sunken Vault" {
		t.Errorf("after set = %+v, want sunken-vault / The Sunken Vault", got)
	}

	// Clearing it.
	clearResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/session/active-campaign",
		map[string]any{"campaign_id": ""}, "")
	if decodeSession(t, clearResp).ActiveCampaignID != "" {
		t.Error("clearing active campaign did not take")
	}
}

func TestSession_SetActiveCampaign_UnknownID_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/session/active-campaign",
		map[string]any{"campaign_id": "does-not-exist"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSession_JoinLock_Toggles(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	lockResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/session/join-lock",
		map[string]any{"locked": true}, "")
	if !decodeSession(t, lockResp).JoinLocked {
		t.Fatal("lock did not take")
	}
	unlockResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/session/join-lock",
		map[string]any{"locked": false}, "")
	if decodeSession(t, unlockResp).JoinLocked {
		t.Error("unlock did not take")
	}
}

func TestSession_MutatingEndpoints_RejectCrossOrigin(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	for _, path := range []string{"/api/session/active-campaign", "/api/session/join-lock"} {
		resp := doJSON(t, http.MethodPut, httpSrv.URL+path, map[string]any{}, "http://evil.example")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s cross-origin: status = %d, want 403", path, resp.StatusCode)
		}
	}
}
