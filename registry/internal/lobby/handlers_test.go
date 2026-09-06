// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package lobby_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/registry/internal/lobby"
)

func newTestServer(t *testing.T) (*httptest.Server, *lobby.Store) {
	t.Helper()
	store := lobby.NewStore()
	ts := httptest.NewServer(lobby.NewHandler(store, time.Minute))
	t.Cleanup(ts.Close)
	return ts, store
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func listingRequestBody() map[string]any {
	return map[string]any{
		"adventure_name":     "The Sable Ravine",
		"min_level":          1,
		"max_level":          3,
		"players_joined":     2,
		"player_slots":       5,
		"password_protected": false,
		"join_url":           "wss://example.com/ws",
		"campaign_id":        "sable-ravine",
	}
}

func TestHandler_PostListings_CreatesAndReturnsIDAndToken(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body = %s", resp.StatusCode, body)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if created.ID == "" || created.Token == "" {
		t.Errorf("response = %+v, want both id and token non-empty", created)
	}
}

func TestHandler_GetListings_ReturnsCreatedListing_WithoutToken(t *testing.T) {
	ts, _ := newTestServer(t)
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody()).Body.Close()

	resp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/listings", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if bytes.Contains(body, []byte("token")) {
		t.Errorf("GET /api/v1/listings response contains \"token\": %s", body)
	}

	var parsed struct {
		Listings []struct {
			ID                string `json:"id"`
			AdventureName     string `json:"adventure_name"`
			MinLevel          int    `json:"min_level"`
			MaxLevel          int    `json:"max_level"`
			PlayersJoined     int    `json:"players_joined"`
			PlayerSlots       int    `json:"player_slots"`
			PasswordProtected bool   `json:"password_protected"`
			JoinURL           string `json:"join_url"`
			CampaignID        string `json:"campaign_id"`
		} `json:"listings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}
	if len(parsed.Listings) != 1 {
		t.Fatalf("Listings = %+v, want exactly 1", parsed.Listings)
	}
	got := parsed.Listings[0]
	if got.AdventureName != "The Sable Ravine" || got.MinLevel != 1 || got.MaxLevel != 3 ||
		got.PlayersJoined != 2 || got.PlayerSlots != 5 || got.JoinURL != "wss://example.com/ws" || got.CampaignID != "sable-ravine" {
		t.Errorf("listing = %+v, want it to match the created fields", got)
	}
}

func TestHandler_PutListing_UpdatesFields(t *testing.T) {
	ts, _ := newTestServer(t)
	createResp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody())
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	update := listingRequestBody()
	update["token"] = created.Token
	update["players_joined"] = 4
	resp := doJSON(t, http.MethodPut, ts.URL+"/api/v1/listings/"+created.ID, update)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}

	getResp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/listings", nil)
	var parsed struct {
		Listings []struct {
			PlayersJoined int `json:"players_joined"`
		} `json:"listings"`
	}
	json.NewDecoder(getResp.Body).Decode(&parsed)
	getResp.Body.Close()
	if len(parsed.Listings) != 1 || parsed.Listings[0].PlayersJoined != 4 {
		t.Errorf("after PUT, listings = %+v, want players_joined=4", parsed.Listings)
	}
}

func TestHandler_PutListing_WrongToken_ReturnsForbidden(t *testing.T) {
	ts, _ := newTestServer(t)
	createResp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody())
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	update := listingRequestBody()
	update["token"] = "wrong-token"
	resp := doJSON(t, http.MethodPut, ts.URL+"/api/v1/listings/"+created.ID, update)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestHandler_PutListing_UnknownID_ReturnsNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	update := listingRequestBody()
	update["token"] = "any-token"
	resp := doJSON(t, http.MethodPut, ts.URL+"/api/v1/listings/does-not-exist", update)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandler_DeleteListing_RemovesIt(t *testing.T) {
	ts, _ := newTestServer(t)
	createResp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody())
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	resp := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/listings/"+created.ID, map[string]any{"token": created.Token})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body = %s", resp.StatusCode, body)
	}

	getResp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/listings", nil)
	var parsed struct {
		Listings []any `json:"listings"`
	}
	json.NewDecoder(getResp.Body).Decode(&parsed)
	getResp.Body.Close()
	if len(parsed.Listings) != 0 {
		t.Errorf("listings after Delete = %+v, want empty", parsed.Listings)
	}
}

func TestHandler_DeleteListing_WrongToken_ReturnsForbidden_AndDoesNotDelete(t *testing.T) {
	ts, _ := newTestServer(t)
	createResp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/listings", listingRequestBody())
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	resp := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/listings/"+created.ID, map[string]any{"token": "wrong-token"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	getResp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/listings", nil)
	var parsed struct {
		Listings []any `json:"listings"`
	}
	json.NewDecoder(getResp.Body).Decode(&parsed)
	getResp.Body.Close()
	if len(parsed.Listings) != 1 {
		t.Errorf("listings after a rejected Delete = %+v, want still 1", parsed.Listings)
	}
}

func TestHandler_GetListings_ExcludesExpiredListing(t *testing.T) {
	store := lobby.NewStore()
	ts := httptest.NewServer(lobby.NewHandler(store, time.Millisecond))
	defer ts.Close()

	if _, _, err := store.Create(lobby.Fields{AdventureName: "Stale"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	resp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/listings", nil)
	defer resp.Body.Close()
	var parsed struct {
		Listings []any `json:"listings"`
	}
	json.NewDecoder(resp.Body).Decode(&parsed)
	if len(parsed.Listings) != 0 {
		t.Errorf("listings = %+v, want empty (past the 1ms TTL)", parsed.Listings)
	}
}
