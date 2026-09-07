// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/terms"
)

func newTestServer(t *testing.T, restartRequested chan struct{}) (*admin.Server, *httptest.Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestStore(t)
	seed := map[string]string{admin.SystemKeyAddr: ":8080", admin.SystemKeyLLMModel: "seed-model"}
	srv := admin.New(logger, s, s, s, s, "", "127.0.0.1:8090", seed, restartRequested, nil, "", "", session.NewHub())
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return srv, httpSrv
}

// newTestServerWithLLM is newTestServer plus a real llm.Provider and a
// real temp-dir campaignPacksDir — for the "Generate a campaign pack
// with AI" endpoints specifically, which reject with a real "not
// configured" error against newTestServer's own nil/empty defaults.
func newTestServerWithLLM(t *testing.T, llmProvider llm.Provider) (*admin.Server, *httptest.Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestStore(t)
	srv := admin.New(logger, s, s, s, s, "", "127.0.0.1:8090", nil, nil, llmProvider, "test-model", t.TempDir(), session.NewHub())
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
		"pvp_policy":                 "pvp_allowed",
		"maturity_tier_prompt":       "Keep it clean.",
		"image_maturity_tier_prompt": "No gore.",
		"price_multiplier":           1.5,
		"min_level":                  3,
		"max_level":                  8,
		"max_players":                6,
		"registry_listed":            true,
		"join_address":               "wss://myhost.example.com/ws",
	}, "")
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d, body = %s", putResp.StatusCode, body)
	}

	getResp := doJSON(t, http.MethodGet, url, nil, "")
	var got struct {
		PvPPolicy          string  `json:"pvp_policy"`
		MaturityTierPrompt string  `json:"maturity_tier_prompt"`
		PriceMultiplier    float64 `json:"price_multiplier"`
		MinLevel           int     `json:"min_level"`
		MaxLevel           int     `json:"max_level"`
		MaxPlayers         int     `json:"max_players"`
		RegistryListed     bool    `json:"registry_listed"`
		JoinAddress        string  `json:"join_address"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PvPPolicy != "pvp_allowed" {
		t.Errorf("PvPPolicy = %q, want pvp_allowed", got.PvPPolicy)
	}
	if got.MaturityTierPrompt != "Keep it clean." {
		t.Errorf("MaturityTierPrompt = %q, want %q", got.MaturityTierPrompt, "Keep it clean.")
	}
	if got.PriceMultiplier != 1.5 {
		t.Errorf("PriceMultiplier = %v, want 1.5", got.PriceMultiplier)
	}
	if got.MinLevel != 3 || got.MaxLevel != 8 {
		t.Errorf("MinLevel/MaxLevel = %d/%d, want 3/8", got.MinLevel, got.MaxLevel)
	}
	if got.MaxPlayers != 6 {
		t.Errorf("MaxPlayers = %d, want 6", got.MaxPlayers)
	}
	if !got.RegistryListed {
		t.Error("RegistryListed = false, want true")
	}
	if got.JoinAddress != "wss://myhost.example.com/ws" {
		t.Errorf("JoinAddress = %q, want %q", got.JoinAddress, "wss://myhost.example.com/ws")
	}
}

func TestServer_PutCampaignPolicy_RegistryListedWithoutJoinAddress_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"registry_listed": true,
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_NegativeMaxPlayers_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"max_players": -1,
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_MinLevelAboveMaxLevel_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"min_level": 10,
		"max_level": 5,
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutCampaignPolicy_NegativeMinLevel_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/policy", map[string]any{
		"min_level": -1,
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
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

func TestServer_PutSystem_LLMProviderAndAPIKey_RoundTrip(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	putResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/system", map[string]any{
		"addr": ":8080", "llm_provider": "anthropic", "llm_api_key": "sk-ant-test", "llm_model": "claude-opus-5",
	}, "")
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", putResp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/system", nil, "")
	var got struct {
		LLMProvider string `json:"llm_provider"`
		LLMAPIKey   string `json:"llm_api_key"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.LLMProvider != "anthropic" || got.LLMAPIKey != "sk-ant-test" {
		t.Errorf("got = %+v, want llm_provider=anthropic llm_api_key=sk-ant-test", got)
	}
}

func TestServer_PutSystem_UnrecognizedProvider_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/system", map[string]any{
		"llm_provider": "bogus",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutSystem_NonOllamaProviderWithoutAPIKey_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/system", map[string]any{
		"llm_provider": "openai",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_PutSystem_OllamaProviderWithoutAPIKey_Allowed(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/system", map[string]any{
		"llm_provider": "ollama", "llm_url": "http://localhost:11434",
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// fakeOllamaServer mimics Ollama's own POST /api/chat contract — used to
// exercise handleTestLLM's real llm.NewProvider->OllamaProvider->real
// HTTP call path against a hermetic server, the same style
// internal/llm's own tests already use, rather than mocking
// llm.Provider directly (this endpoint deliberately constructs its own
// ad-hoc, not-yet-saved provider from the request body, so there's no
// injection point for a fake Provider to substitute in even if we
// wanted one).
func fakeOllamaServer(t *testing.T, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected request path %q, want /api/chat", r.URL.Path)
		}
		respond(w)
	}))
}

func TestServer_TestLLM_RealSuccessfulCall_ReturnsOK(t *testing.T) {
	fake := fakeOllamaServer(t, func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "OK"},
			"done":    true,
		})
	})
	defer fake.Close()
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm", map[string]any{
		"llm_provider": "ollama", "llm_url": fake.URL, "llm_model": "test-model",
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got struct {
		OK       bool   `json:"ok"`
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.OK || got.Response != "OK" {
		t.Errorf("got = %+v, want ok=true response=OK", got)
	}
}

func TestServer_TestLLM_ProviderCallFails_ReturnsBadGatewayWithDetail(t *testing.T) {
	fake := fakeOllamaServer(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer fake.Close()
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm", map[string]any{
		"llm_provider": "ollama", "llm_url": fake.URL, "llm_model": "test-model",
	}, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	var got struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Error == "" {
		t.Error("error message is empty, want a real detail for the Host")
	}
}

func TestServer_TestLLM_OllamaMissingURL_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm",
		map[string]any{"llm_provider": "ollama"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_TestLLM_NonOllamaMissingAPIKey_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm",
		map[string]any{"llm_provider": "openai"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_TestLLM_UnrecognizedProvider_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm",
		map[string]any{"llm_provider": "bogus"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_TestLLM_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/system/test-llm",
		map[string]any{"llm_provider": "ollama", "llm_url": "http://localhost:11434"}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestServer_GetTerms_NotYetAccepted_ReturnsUnaccepted(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/terms", nil, "")
	var got struct {
		Version      string `json:"version"`
		OperatorText string `json:"operator_text"`
		Accepted     bool   `json:"accepted"`
		AcceptedAt   string `json:"accepted_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Version != terms.Version {
		t.Errorf("Version = %q, want %q", got.Version, terms.Version)
	}
	if got.OperatorText == "" {
		t.Error("OperatorText is empty, want the real disclaimer text")
	}
	if got.Accepted {
		t.Error("Accepted = true, want false before anyone has accepted")
	}
	if got.AcceptedAt != "" {
		t.Errorf("AcceptedAt = %q, want empty before acceptance", got.AcceptedAt)
	}
}

func TestServer_PostTermsAccept_ThenGetTerms_ReturnsAccepted(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	postResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/terms/accept", nil, "")
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/terms/accept status = %d, want 200", postResp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/terms", nil, "")
	var got struct {
		Accepted   bool   `json:"accepted"`
		AcceptedAt string `json:"accepted_at"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.Accepted {
		t.Error("Accepted = false, want true after POST /api/terms/accept")
	}
	if got.AcceptedAt == "" {
		t.Error("AcceptedAt is empty, want a real timestamp after acceptance")
	}
}

func TestServer_PostTermsAccept_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/terms/accept", nil, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
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

// fakeLLMProvider is a minimal llm.Provider fake local to this
// package's own tests — internal/campaignpack's own fake (used to unit
// test Generate directly) isn't importable from here.
type fakeLLMProvider struct {
	response llm.CompletionResponse
	err      error
}

func (f *fakeLLMProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	if f.err != nil {
		return llm.CompletionResponse{}, f.err
	}
	return f.response, nil
}

func writePackToolCallResponse(t *testing.T, files map[string]string) llm.CompletionResponse {
	t.Helper()
	type wireFile struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	var wireFiles []wireFile
	for path, content := range files {
		wireFiles = append(wireFiles, wireFile{Path: path, Content: content})
	}
	args, err := json.Marshal(map[string]any{"files": wireFiles})
	if err != nil {
		t.Fatalf("marshaling tool call args: %v", err)
	}
	return llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "write_campaign_pack", Arguments: args}},
	}
}

func wellFormedGeneratedPackResponse(t *testing.T) llm.CompletionResponse {
	t.Helper()
	return writePackToolCallResponse(t, map[string]string{
		"campaign.md":                     "---\nid: haunted-lighthouse\ntitle: The Drowned Light\n---\nA storm-battered lighthouse.\n",
		"locations/lighthouse-base.md":    "---\nid: lighthouse-base\n---\nThe base.\n",
		"npcs/keeper-mara.md":             "---\nid: keeper-mara\n---\nThe keeper.\n",
		"encounters/the-drowned-thing.md": "---\nid: the-drowned-thing\n---\nSomething rises.\n",
	})
}

func TestServer_GenerateCampaignPack_NoLLMConfigured_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServer(t, nil) // no LLM provider wired

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse.", "min_level": 1, "max_level": 3}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_GenerateCampaignPack_WellFormedGeneration_ReturnsFiles(t *testing.T) {
	provider := &fakeLLMProvider{response: wellFormedGeneratedPackResponse(t)}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse on a storm-battered coast.", "min_level": 1, "max_level": 3}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got struct {
		Slug  string `json:"slug"`
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Slug != "a-haunted-lighthouse-on-a-storm-battered-coast" {
		t.Errorf("Slug = %q, want a description-derived slug", got.Slug)
	}
	if len(got.Files) != 4 {
		t.Errorf("len(Files) = %d, want 4", len(got.Files))
	}
}

func TestServer_GenerateCampaignPack_ModelDidNotCallTool_ReturnsBadGatewayWithDetail(t *testing.T) {
	provider := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Sure, here's an idea..."}}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse."}, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	var got struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Error == "" {
		t.Error("error message is empty, want a real detail for the Host")
	}
}

// TestServer_GenerateCampaignPack_MalformedFrontMatter_ReturnsFilesWithValidationError
// covers a real, live-observed model quirk: one generated file's YAML
// front matter had a syntax mistake, which would previously discard the
// entire multi-minute generation with only an error message — the Host
// must still get the files back for review/editing instead. Uses an
// unclosed quote specifically (not the "unquoted colon in a scalar
// value" quirk — campaignpack.Generate auto-repairs that one now, see
// generate.go's repairUnquotedColonInScalarValues) so this still
// exercises a genuinely unfixed parse failure.
func TestServer_GenerateCampaignPack_MalformedFrontMatter_ReturnsFilesWithValidationError(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCallResponse(t, map[string]string{
		"campaign.md":        "---\nid: haunted-lighthouse\n---\nA storm-battered lighthouse.\n",
		"npcs/tide-witch.md": "---\nid: tide-witch\nvoice: \"she speaks in half-finished sentences\n---\nThe tide witch.\n",
	})}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse."}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s (a fixable validation issue must not become a hard error)", resp.StatusCode, body)
	}
	var got struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		ValidationError string `json:"validation_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Files) != 2 {
		t.Errorf("len(Files) = %d, want 2 (both files returned for review despite one being broken)", len(got.Files))
	}
	if got.ValidationError == "" {
		t.Error("ValidationError is empty, want a real detail about the malformed front matter")
	}
}

func TestServer_GenerateCampaignPack_CrossOriginRequest_Rejected(t *testing.T) {
	provider := &fakeLLMProvider{response: wellFormedGeneratedPackResponse(t)}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse."}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestServer_SaveCampaignPack_ThenBindViaExistingPutPackEndpoint(t *testing.T) {
	provider := &fakeLLMProvider{response: wellFormedGeneratedPackResponse(t)}
	_, httpSrv := newTestServerWithLLM(t, provider)

	genResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate",
		map[string]any{"description": "A haunted lighthouse."}, "")
	var generated struct {
		Slug  string `json:"slug"`
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(genResp.Body).Decode(&generated); err != nil {
		t.Fatalf("decoding generate response: %v", err)
	}

	saveResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save",
		map[string]any{"slug": generated.Slug, "files": generated.Files}, "")
	if saveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(saveResp.Body)
		t.Fatalf("save status = %d, body = %s", saveResp.StatusCode, body)
	}
	var saved struct {
		PackDir string `json:"pack_dir"`
	}
	if err := json.NewDecoder(saveResp.Body).Decode(&saved); err != nil {
		t.Fatalf("decoding save response: %v", err)
	}
	if saved.PackDir == "" {
		t.Fatal("PackDir is empty")
	}

	// Prove the reuse claim for real: bind saved.PackDir via the
	// existing, unmodified PUT /api/campaigns/{id}/pack — no new bind
	// logic exists anywhere for a generated pack.
	bindResp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/pack",
		map[string]any{"pack_dir": saved.PackDir}, "")
	if bindResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(bindResp.Body)
		t.Fatalf("bind status = %d, body = %s", bindResp.StatusCode, body)
	}
	var bound struct {
		PackID string `json:"pack_id"`
	}
	if err := json.NewDecoder(bindResp.Body).Decode(&bound); err != nil {
		t.Fatalf("decoding bind response: %v", err)
	}
	if bound.PackID != "haunted-lighthouse" {
		t.Errorf("PackID = %q, want haunted-lighthouse", bound.PackID)
	}
}

func TestServer_SaveCampaignPack_DisallowedFilePath_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save", map[string]any{
		"slug": "malicious",
		"files": []map[string]any{
			{"path": "campaign.md", "content": "---\nid: x\n---\nx\n"},
			{"path": "../../etc/passwd", "content": "malicious"},
		},
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_SaveCampaignPack_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save",
		map[string]any{"slug": "x", "files": []map[string]any{{"path": "campaign.md", "content": "x"}}}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// existingSavedPackWithChapters builds a real, valid on-disk pack (via
// campaignpack.WriteAndValidate, not a hand-rolled fixture) with a
// two-chapter outline and one already-authored chapter-1 location —
// the base fixture for Mode "chapter"/"side_quest" round-trip tests,
// which operate on an already-saved pack_dir rather than creating one.
func existingSavedPackWithChapters(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir, err := campaignpack.WriteAndValidate(root, "the-sunken-vault", []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: `---
id: the-sunken-vault
title: The Sunken Vault
chapters:
  - id: chapter-1
    title: "The Flooded Gate"
    level_range: "1-2"
    summary: "The party finds the vault's entrance underwater."
  - id: chapter-2
    title: "The Drowned Halls"
    level_range: "2-4"
    summary: "The party explores the vault's flooded interior."
---
An ancient vault, sealed beneath a lake, is finally surfacing.
`},
		{Path: "locations/lake-shore.md", Content: "---\nid: lake-shore\nchapter: chapter-1\n---\nThe muddy shore.\n"},
	})
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}
	return dir
}

func TestServer_GenerateCampaignPack_ChapterMode_ReturnsFilesTaggedWithChapter(t *testing.T) {
	packDir := existingSavedPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCallResponse(t, map[string]string{
		"locations/flooded-tunnel.md": "---\nid: flooded-tunnel\nchapter: chapter-2\n---\nA flooded tunnel.\n",
		"encounters/the-guardian.md":  "---\nid: the-guardian\nchapter: chapter-2\n---\nSomething guards the tunnel.\n",
	})}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate", map[string]any{
		"mode": "chapter", "pack_dir": packDir, "chapter_id": "chapter-2",
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
		ValidationError string `json:"validation_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ValidationError != "" {
		t.Errorf("ValidationError = %q, want empty (merges cleanly with the existing pack)", got.ValidationError)
	}
	if len(got.Files) != 2 {
		t.Errorf("len(Files) = %d, want 2", len(got.Files))
	}
}

func TestServer_GenerateCampaignPack_ChapterMode_MissingPackDir_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate", map[string]any{
		"mode": "chapter", "chapter_id": "chapter-2",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (pack_dir is required)", resp.StatusCode)
	}
}

func TestServer_GenerateCampaignPack_SideQuestMode_ReturnsFiles(t *testing.T) {
	packDir := existingSavedPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCallResponse(t, map[string]string{
		"encounters/lost-cart.md": "---\nid: lost-cart\nside_quest: sq-lost-cart\nmin_players: 1\nmax_players: 3\n---\nA cart stuck in the mud.\n",
	})}
	_, httpSrv := newTestServerWithLLM(t, provider)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate", map[string]any{
		"mode": "side_quest", "pack_dir": packDir, "description": "A quick roadside distraction.", "max_players": 3,
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got struct {
		Files           []struct{ Path, Content string }
		ValidationError string `json:"validation_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ValidationError != "" {
		t.Errorf("ValidationError = %q, want empty", got.ValidationError)
	}
}

func TestServer_GenerateCampaignPack_InvalidMode_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate", map[string]any{
		"mode": "not-a-real-mode",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestServer_SaveCampaignPack_ChapterMode_MergesIntoExistingPackDir is
// the real end-to-end round trip the plan calls for: generate a chapter
// via a fake LLM provider, save it, then confirm via LoadPack (loaded
// straight from disk, not through another endpoint) that the merged
// pack genuinely has both the pre-existing and the new content.
func TestServer_SaveCampaignPack_ChapterMode_MergesIntoExistingPackDir(t *testing.T) {
	packDir := existingSavedPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCallResponse(t, map[string]string{
		"locations/flooded-tunnel.md": "---\nid: flooded-tunnel\nchapter: chapter-2\n---\nA flooded tunnel.\n",
	})}
	_, httpSrv := newTestServerWithLLM(t, provider)

	genResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/generate", map[string]any{
		"mode": "chapter", "pack_dir": packDir, "chapter_id": "chapter-2",
	}, "")
	var generated struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(genResp.Body).Decode(&generated); err != nil {
		t.Fatalf("decoding generate response: %v", err)
	}

	saveResp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save", map[string]any{
		"mode": "chapter", "pack_dir": packDir, "files": generated.Files,
	}, "")
	if saveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(saveResp.Body)
		t.Fatalf("save status = %d, body = %s", saveResp.StatusCode, body)
	}

	pack, err := campaignpack.LoadPack(packDir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v", err)
	}
	if pack.ID != "the-sunken-vault" {
		t.Errorf("pack.ID = %q, want the-sunken-vault (pre-existing content must survive)", pack.ID)
	}
	if len(pack.Locations) != 2 {
		t.Errorf("len(Locations) = %d, want 2 (1 pre-existing + 1 new)", len(pack.Locations))
	}
}

func TestServer_SaveCampaignPack_ChapterMode_MissingPackDir_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save", map[string]any{
		"mode":  "chapter",
		"files": []map[string]any{{"path": "locations/x.md", "content": "---\nid: x\n---\nx\n"}},
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (pack_dir is required)", resp.StatusCode)
	}
}

func TestServer_SaveCampaignPack_InvalidMode_ReturnsBadRequest(t *testing.T) {
	_, httpSrv := newTestServerWithLLM(t, &fakeLLMProvider{})

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaign-packs/save", map[string]any{
		"mode":  "not-a-real-mode",
		"files": []map[string]any{{"path": "campaign.md", "content": "x"}},
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_Health_ReturnsOK(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
