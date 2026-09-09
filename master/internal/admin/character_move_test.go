// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestServer_ListAllCharacters_ReturnsEveryCampaign(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "c1", CampaignID: "camp-a", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})
	seedCharacter(t, s, store.Character{
		ID: "c2", CampaignID: "camp-b", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{"name":"Bram"}`),
	})

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/characters", nil, "")
	var got []struct {
		ID, CampaignID, OwnerID, Status, Name string
	}
	// json tags are snake_case; decode with explicit tags.
	var raw []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, m := range raw {
		got = append(got, struct{ ID, CampaignID, OwnerID, Status, Name string }{
			m["id"].(string), m["campaign_id"].(string), m["owner_id"].(string), m["status"].(string), m["name"].(string),
		})
	}
	if len(got) != 2 {
		t.Fatalf("ListAllCharacters = %+v, want 2 across both campaigns", got)
	}
	byID := map[string]struct{ CampaignID, Name string }{}
	for _, g := range got {
		byID[g.ID] = struct{ CampaignID, Name string }{g.CampaignID, g.Name}
	}
	if byID["c1"].CampaignID != "camp-a" || byID["c1"].Name != "Kestrel" {
		t.Errorf("c1 = %+v, want camp-a / Kestrel", byID["c1"])
	}
	if byID["c2"].CampaignID != "camp-b" || byID["c2"].Name != "Bram" {
		t.Errorf("c2 = %+v, want camp-b / Bram", byID["c2"])
	}
}

func TestServer_MoveCharacter_ReassignsCampaign(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "c1", CampaignID: "camp-a", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/characters/c1/campaign",
		map[string]any{"campaign_id": "camp-b"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, err := s.GetCharacter(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CampaignID != "camp-b" || got.Status != store.CharacterStatusApproved {
		t.Errorf("after move: campaign=%q status=%q, want camp-b / approved", got.CampaignID, got.Status)
	}
}

func TestServer_MoveCharacter_Rejections(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "c1", CampaignID: "camp-a", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	if r := doJSON(t, http.MethodPut, httpSrv.URL+"/api/characters/c1/campaign",
		map[string]any{"campaign_id": ""}, ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("empty campaign_id status = %d, want 400", r.StatusCode)
	}
	if r := doJSON(t, http.MethodPut, httpSrv.URL+"/api/characters/nope/campaign",
		map[string]any{"campaign_id": "camp-b"}, ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown character status = %d, want 404", r.StatusCode)
	}
	if r := doJSON(t, http.MethodPut, httpSrv.URL+"/api/characters/c1/campaign",
		map[string]any{"campaign_id": "camp-b"}, "http://evil.example"); r.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin status = %d, want 403", r.StatusCode)
	}
}

func TestServer_DeleteCharacter_RemovesRecord(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "c1", CampaignID: "camp-a", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodDelete, httpSrv.URL+"/api/characters/c1", nil, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := s.GetCharacter(context.Background(), "c1"); err == nil {
		t.Error("character still present after delete")
	}
	if r := doJSON(t, http.MethodDelete, httpSrv.URL+"/api/characters/c1", nil, ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", r.StatusCode)
	}
}
