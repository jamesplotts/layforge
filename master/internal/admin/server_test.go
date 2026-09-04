// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/admin"
)

func newTestServer(t *testing.T, restartRequested chan struct{}) (*admin.Server, *httptest.Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestStore(t)
	seed := map[string]string{admin.SystemKeyAddr: ":8080", admin.SystemKeyLLMModel: "seed-model"}
	srv := admin.New(logger, s, s, "", "127.0.0.1:8090", seed, restartRequested)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return srv, httpSrv
}

func doJSON(t *testing.T, method, url string, body any, origin string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestServer_ListCampaigns_NoneKnown_ReturnsEmptyList(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Campaigns) != 0 {
		t.Errorf("Campaigns = %v, want empty", got.Campaigns)
	}
}

func TestServer_CreateCampaign_ThenListShowsRealDisplayNameAndDefaults(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	createResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns", map[string]any{
		"campaign_id": "campaign-1", "display_name": "The Iron Crown",
	}, "")
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("POST status = %d, body = %s", createResp.StatusCode, body)
	}

	listResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	var got struct {
		Campaigns []struct {
			CampaignID   string `json:"campaign_id"`
			DisplayName  string `json:"display_name"`
			PartyCount   int    `json:"party_count"`
			LastActiveAt string `json:"last_active_at"`
			Archived     bool   `json:"archived"`
		} `json:"campaigns"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Campaigns) != 1 {
		t.Fatalf("len(Campaigns) = %d, want 1", len(got.Campaigns))
	}
	c := got.Campaigns[0]
	if c.CampaignID != "campaign-1" || c.DisplayName != "The Iron Crown" {
		t.Errorf("campaign = %+v, want campaign_id=campaign-1 display_name=%q", c, "The Iron Crown")
	}
	if c.PartyCount != 0 {
		t.Errorf("PartyCount = %d, want 0", c.PartyCount)
	}
	if c.LastActiveAt != "" {
		t.Errorf("LastActiveAt = %q, want empty (nobody has joined yet)", c.LastActiveAt)
	}
	if c.Archived {
		t.Error("Archived = true, want false (default)")
	}
}

func TestServer_CreateCampaign_MissingCampaignID_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns", map[string]any{"display_name": "no id"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_CreateCampaign_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns",
		map[string]any{"campaign_id": "campaign-1", "display_name": "x"}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestServer_PutCampaignArchived_TogglesAndReflectsInList(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	createResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns", map[string]any{
		"campaign_id": "campaign-1", "display_name": "The Iron Crown",
	}, "")
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", createResp.StatusCode)
	}

	archiveResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/archive", map[string]any{"archived": true}, "")
	if archiveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(archiveResp.Body)
		t.Fatalf("PUT archive status = %d, body = %s", archiveResp.StatusCode, body)
	}

	listResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	var got struct {
		Campaigns []struct {
			CampaignID  string `json:"campaign_id"`
			DisplayName string `json:"display_name"`
			Archived    bool   `json:"archived"`
		} `json:"campaigns"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Campaigns) != 1 || !got.Campaigns[0].Archived {
		t.Fatalf("Campaigns = %+v, want a single archived campaign", got.Campaigns)
	}
	if got.Campaigns[0].DisplayName != "The Iron Crown" {
		t.Errorf("DisplayName = %q, want %q (archiving must not clear it)", got.Campaigns[0].DisplayName, "The Iron Crown")
	}
}

func TestServer_PutCampaignArchived_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/archive",
		map[string]any{"archived": true}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestServer_DeleteCampaign_FullRoundTrip_RemovesFromList(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	createResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns", map[string]any{
		"campaign_id": "campaign-1", "display_name": "Doomed Campaign",
	}, "")
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", createResp.StatusCode)
	}
	archiveResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/archive", map[string]any{"archived": true}, "")
	if archiveResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT archive status = %d", archiveResp.StatusCode)
	}

	deleteResp := doJSON(t, http.MethodDelete, httpSrv.URL+"/api/campaigns/campaign-1", nil, "")
	if deleteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deleteResp.Body)
		t.Fatalf("DELETE status = %d, body = %s", deleteResp.StatusCode, body)
	}

	listResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	var got struct {
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Campaigns) != 0 {
		t.Errorf("Campaigns = %v, want empty after delete", got.Campaigns)
	}
}

func TestServer_DeleteCampaign_NotArchived_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	createResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns", map[string]any{
		"campaign_id": "campaign-1", "display_name": "Still Live",
	}, "")
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", createResp.StatusCode)
	}

	deleteResp := doJSON(t, http.MethodDelete, httpSrv.URL+"/api/campaigns/campaign-1", nil, "")
	if deleteResp.StatusCode != http.StatusBadRequest {
		t.Errorf("DELETE status = %d, want 400 (campaign was never archived)", deleteResp.StatusCode)
	}

	listResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns", nil, "")
	var got struct {
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Campaigns) != 1 {
		t.Errorf("Campaigns = %v, want the rejected delete to have left it in place", got.Campaigns)
	}
}

func TestServer_DeleteCampaign_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodDelete, httpSrv.URL+"/api/campaigns/campaign-1", nil, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestServer_PutThenGetCampaignPolicy_RoundTrips(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	url := httpSrv.URL + "/api/campaigns/campaign-1/policy"

	putResp := doJSON(t, http.MethodPut, url, map[string]any{
		"pvp_policy":                 "pvp_with_consent",
		"pvp_consent":                []string{"player-a"},
		"maturity_tier_prompt":       "Keep it clean.",
		"image_maturity_tier_prompt": "No gore.",
		"price_multiplier":           1.5,
	}, "")
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d, body = %s", putResp.StatusCode, body)
	}

	getResp := doJSON(t, http.MethodGet, url, nil, "")
	var got struct {
		PvPPolicy          string   `json:"pvp_policy"`
		PvPConsent         []string `json:"pvp_consent"`
		MaturityTierPrompt string   `json:"maturity_tier_prompt"`
		PriceMultiplier    float64  `json:"price_multiplier"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PvPPolicy != "pvp_with_consent" {
		t.Errorf("PvPPolicy = %q, want pvp_with_consent", got.PvPPolicy)
	}
	if len(got.PvPConsent) != 1 || got.PvPConsent[0] != "player-a" {
		t.Errorf("PvPConsent = %v, want [player-a]", got.PvPConsent)
	}
	if got.MaturityTierPrompt != "Keep it clean." {
		t.Errorf("MaturityTierPrompt = %q, want %q", got.MaturityTierPrompt, "Keep it clean.")
	}
	if got.PriceMultiplier != 1.5 {
		t.Errorf("PriceMultiplier = %v, want 1.5", got.PriceMultiplier)
	}
}

func TestServer_PutCampaignPolicy_NegativePriceMultiplier_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"price_multiplier": -1.0,
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_InvalidPvPPolicy_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"pvp_policy": "not_a_real_policy",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_DoesNotClobberSecuritySetOnTheSameCampaign(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	base := httpSrv.URL + "/api/campaigns/campaign-1"

	secResp := doJSON(t, http.MethodPut, base+"/security", map[string]any{"room_password": "hunter2"}, "")
	if secResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT security status = %d", secResp.StatusCode)
	}
	polResp := doJSON(t, http.MethodPut, base+"/policy", map[string]any{"pvp_policy": "pvp_allowed"}, "")
	if polResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT policy status = %d", polResp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, base+"/security", nil, "")
	var got struct {
		RoomPassword string `json:"room_password"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.RoomPassword != "hunter2" {
		t.Errorf("RoomPassword = %q, want hunter2 to survive the later policy PUT", got.RoomPassword)
	}
}

func TestServer_GetSystem_NoOverridesSaved_ReturnsSeedValues(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/system", nil, "")
	var got struct {
		Addr     string `json:"addr"`
		LLMModel string `json:"llm_model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Addr != ":8080" || got.LLMModel != "seed-model" {
		t.Errorf("got = %+v, want seed values (:8080, seed-model)", got)
	}
}

func TestServer_PutSystem_OverridesSeedValue(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	putResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/system", map[string]any{
		"addr": ":8080", "llm_model": "overridden-model",
	}, "")
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", putResp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/system", nil, "")
	var got struct {
		LLMModel string `json:"llm_model"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.LLMModel != "overridden-model" {
		t.Errorf("LLMModel = %q, want overridden-model", got.LLMModel)
	}
}

func TestServer_Restart_SignalsChannelAfterResponding(t *testing.T) {
	restartRequested := make(chan struct{}, 1)
	_, httpSrv := newTestServer(t, restartRequested)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/restart", nil, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	select {
	case <-restartRequested:
	case <-time.After(2 * time.Second):
		t.Fatal("restartRequested was never signaled")
	}
}

func TestServer_Restart_WithBody_PersistsSettingsBeforeSignaling(t *testing.T) {
	restartRequested := make(chan struct{}, 1)
	_, httpSrv := newTestServer(t, restartRequested)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/restart", map[string]any{
		"llm_model": "restart-saved-model",
	}, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	<-restartRequested

	getResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/system", nil, "")
	var got struct {
		LLMModel string `json:"llm_model"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.LLMModel != "restart-saved-model" {
		t.Errorf("LLMModel = %q, want the value POSTed to /restart to have been saved first", got.LLMModel)
	}
}

func TestServer_PutCampaignPolicy_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy",
		map[string]any{"pvp_policy": "pvp_allowed"}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_SameOriginRequest_Allowed(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy",
		map[string]any{"pvp_policy": "pvp_allowed"}, "http://127.0.0.1:8090")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for a same-origin request", resp.StatusCode)
	}
}

// sableRavinePackDir is the real, committed example pack — see
// campaignpack's own loader_test.go for the same fixture, and
// internal/server's location_test.go for its own copy of this constant
// (each package keeps its own relative path from its own directory).
const sableRavinePackDir = "../../../campaign-packs/sable-ravine"

func TestServer_PutThenGetCampaignPack_RoundTrips(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	url := httpSrv.URL + "/api/campaigns/campaign-1/pack"

	putResp := doJSON(t, http.MethodPut, url, map[string]any{"pack_dir": sableRavinePackDir}, "")
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d, body = %s", putResp.StatusCode, body)
	}

	getResp := doJSON(t, http.MethodGet, url, nil, "")
	var got struct {
		PackDir string `json:"pack_dir"`
		PackID  string `json:"pack_id"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PackDir != sableRavinePackDir {
		t.Errorf("PackDir = %q, want %q", got.PackDir, sableRavinePackDir)
	}
	if got.PackID != "sable-ravine" {
		t.Errorf("PackID = %q, want %q (parsed from the real campaign.md)", got.PackID, "sable-ravine")
	}
}

func TestServer_GetCampaignPack_NeverBound_ReturnsEmpty(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/campaign-never-bound/pack", nil, "")
	var got struct {
		PackDir string `json:"pack_dir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PackDir != "" {
		t.Errorf("PackDir = %q, want empty", got.PackDir)
	}
}

func TestServer_PutCampaignPack_DirectoryDoesNotParse_ReturnsBadRequestAndBindsNothing(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	url := httpSrv.URL + "/api/campaigns/campaign-1/pack"

	resp := doJSON(t, http.MethodPut, url, map[string]any{"pack_dir": t.TempDir()}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a directory with no campaign.md", resp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, url, nil, "")
	var got struct {
		PackDir string `json:"pack_dir"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PackDir != "" {
		t.Errorf("PackDir = %q, want empty (a rejected bind must not partially apply)", got.PackDir)
	}
}

func TestServer_PutCampaignPack_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/pack",
		map[string]any{"pack_dir": sableRavinePackDir}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}

func TestServer_Health_ReturnsOK(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
