// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// newTestServerWithStore is newTestServer's twin for the character-review
// tests specifically — they need to seed a store.Character directly
// (something a player.upload would normally do) before exercising the
// admin endpoints against it, so this returns the underlying store too.
func newTestServerWithStore(t *testing.T) (*admin.Server, *httptest.Server, *store.SQLiteEventStore, *session.Hub) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestStore(t)
	hub := session.NewHub()
	srv := admin.New(logger, s, s, s, s, "", "127.0.0.1:8090", nil, nil, hub)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return srv, httpSrv, s, hub
}

func seedCharacter(t *testing.T, s *store.SQLiteEventStore, character store.Character) {
	t.Helper()
	if character.CreatedAt.IsZero() {
		character.CreatedAt = time.Now().UTC()
	}
	if character.UpdatedAt.IsZero() {
		character.UpdatedAt = character.CreatedAt
	}
	if err := s.SaveCharacter(context.Background(), character); err != nil {
		t.Fatalf("seedCharacter: SaveCharacter() error = %v", err)
	}
}

func TestServer_ListCharacters_ReturnsEveryCharacterForTheCampaign(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})
	seedCharacter(t, s, store.Character{
		ID: "char-2", CampaignID: "campaign-1", OwnerID: "player-b",
		Status: store.CharacterStatusApproved, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Bram"}`),
	})
	seedCharacter(t, s, store.Character{
		ID: "char-other-campaign", CampaignID: "campaign-2", OwnerID: "player-c",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Elsewhere"}`),
	})

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/campaign-1/characters", nil, "")
	var got []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListCharacters response = %+v, want exactly the 2 characters in campaign-1", got)
	}
}

func TestServer_ListCharacters_NotConfigured_ReturnsEmptyList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := admin.New(logger, newTestStore(t), nil, nil, nil, "", "127.0.0.1:8090", nil, nil, nil)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp := doJSON(t, http.MethodGet, httpSrv.URL+"/api/campaigns/campaign-1/characters", nil, "")
	var got []any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListCharacters response = %v, want empty", got)
	}
}

func TestServer_ReviewCharacter_Approve_UpdatesStatus(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "approved",
		"reason": "Looks fine for this table.",
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body = %s", resp.StatusCode, body)
	}

	got, err := s.GetCharacter(context.Background(), "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if got.Status != store.CharacterStatusApproved {
		t.Errorf("Status = %q, want %q", got.Status, store.CharacterStatusApproved)
	}
}

func TestServer_ReviewCharacter_Reject_UpdatesStatus(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	// A Host's own rejection overrides even an already-approved
	// character — the Host is this system's final authority (see
	// handleReviewCharacter's own doc comment).
	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "rejected",
		"reason": "Changed my mind about this one.",
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body = %s", resp.StatusCode, body)
	}

	got, err := s.GetCharacter(context.Background(), "char-1")
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if got.Status != store.CharacterStatusRejected {
		t.Errorf("Status = %q, want %q", got.Status, store.CharacterStatusRejected)
	}
}

func TestServer_ReviewCharacter_StatusPendingReview_ReturnsBadRequest(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "pending_review",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_ReviewCharacter_InvalidStatus_ReturnsBadRequest(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "not_a_real_status",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_ReviewCharacter_WrongCampaign_ReturnsNotFound(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-2/characters/char-1/review", map[string]any{
		"status": "approved",
	}, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestServer_ReviewCharacter_NotConfigured_ReturnsBadRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := admin.New(logger, newTestStore(t), nil, nil, nil, "", "127.0.0.1:8090", nil, nil, nil)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "approved",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_ReviewCharacter_CrossOriginRequest_Rejected(t *testing.T) {
	_, httpSrv, s, _ := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "approved",
	}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// TestServer_ReviewCharacter_PushesLiveNotificationToOwner is the
// regression coverage for handleReviewCharacter's hub.SendToSender call
// — a Host's decision must reach a connected player immediately, not
// just update the database. Registers a fake client the same way
// package session's own tests do (via Hub.Register), then asserts its
// outbox receives a character.review_result naming the right
// character_id/status/reason.
func TestServer_ReviewCharacter_PushesLiveNotificationToOwner(t *testing.T) {
	_, httpSrv, s, hub := newTestServerWithStore(t)
	seedCharacter(t, s, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusPendingReview, SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Kestrel"}`),
	})
	client := hub.Register("campaign-1", "player-a")
	defer hub.Unregister(client)

	resp := doJSON(t, http.MethodPut, httpSrv.URL+"/api/campaigns/campaign-1/characters/char-1/review", map[string]any{
		"status": "approved",
		"reason": "Welcome to the table.",
	}, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body = %s", resp.StatusCode, body)
	}

	select {
	case payload := <-client.Outbox():
		var msg struct {
			Type    string `json:"type"`
			Payload struct {
				CharacterID string `json:"character_id"`
				Status      string `json:"status"`
				Reason      string `json:"reason"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("unmarshaling pushed message: %v", err)
		}
		if msg.Type != "character.review_result" {
			t.Errorf("Type = %q, want character.review_result", msg.Type)
		}
		if msg.Payload.CharacterID != "char-1" || msg.Payload.Status != "approved" || msg.Payload.Reason != "Welcome to the table." {
			t.Errorf("payload = %+v, want character_id=char-1, status=approved, reason=%q", msg.Payload, "Welcome to the table.")
		}
	default:
		t.Fatal("expected a character.review_result pushed to the owner's outbox, got nothing")
	}
}
