// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestServer_ListConnectedPlayers_ReturnsDistinctSenderIDs(t *testing.T) {
	_, httpSrv, _, hub := newTestServerWithStore(t)
	hub.Register("campaign-1", "player-b")
	hub.Register("campaign-1", "player-a")
	hub.Register("campaign-2", "player-c") // different campaign, must not appear

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/campaign-1/players", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		SenderIDs []string `json:"sender_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	want := []string{"player-a", "player-b"}
	if len(got.SenderIDs) != len(want) {
		t.Fatalf("SenderIDs = %v, want %v", got.SenderIDs, want)
	}
	for i := range want {
		if got.SenderIDs[i] != want[i] {
			t.Errorf("SenderIDs[%d] = %q, want %q", i, got.SenderIDs[i], want[i])
		}
	}
}

func TestServer_KickPlayer_ConnectedSender_ClosesConnectionAndReturnsKickedTrue(t *testing.T) {
	_, httpSrv, _, hub := newTestServerWithStore(t)
	client := hub.Register("campaign-1", "player-a")
	var closed bool
	hub.SetCloser(client, func() { closed = true })

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns/campaign-1/players/player-a/kick", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Kicked bool `json:"kicked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.Kicked {
		t.Error("Kicked = false, want true for a connected sender")
	}
	if !closed {
		t.Error("closer was not invoked")
	}
}

func TestServer_KickPlayer_NotConnected_ReturnsKickedFalseNotAnError(t *testing.T) {
	_, httpSrv, _, _ := newTestServerWithStore(t)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns/campaign-1/players/nobody-connected/kick", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (not connected is not an error)", resp.StatusCode)
	}
	var got struct {
		Kicked bool `json:"kicked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Kicked {
		t.Error("Kicked = true, want false for a sender with no live connection")
	}
}

func TestServer_KickPlayer_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv, _, _ := newTestServerWithStore(t)

	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/campaigns/campaign-1/players/player-a/kick", nil, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-origin request", resp.StatusCode)
	}
}
