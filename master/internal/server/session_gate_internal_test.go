// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func gateTestServer(t *testing.T) (*Server, *store.SQLiteEventStore) {
	t.Helper()
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(nopWriter{}, nil))
	srv := New(logger, st, nil, "", nil, nil, st, nil, nil, st, st, st, nil, st, st, st, session.NewHub())
	return srv, st
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestSessionGate(t *testing.T) {
	ctx := context.Background()

	t.Run("no active campaign allows any join", func(t *testing.T) {
		srv, _ := gateTestServer(t)
		ok, _, err := srv.sessionGate(ctx, "anything", "player-a")
		if err != nil || !ok {
			t.Fatalf("gate = (%v, err %v), want allowed", ok, err)
		}
	})

	t.Run("active campaign set: only that campaign joinable", func(t *testing.T) {
		srv, st := gateTestServer(t)
		if err := st.SaveSystemSettings(ctx, map[string]string{adminSettingsKeyActiveCampaignID: "the-vault"}); err != nil {
			t.Fatal(err)
		}
		if ok, _, _ := srv.sessionGate(ctx, "the-vault", "player-a"); !ok {
			t.Error("active campaign should be joinable")
		}
		if ok, reason, _ := srv.sessionGate(ctx, "some-other", "player-a"); ok || reason == "" {
			t.Errorf("non-active campaign: ok=%v reason=%q, want refused", ok, reason)
		}
	})

	t.Run("locked: newcomer refused, existing character admitted", func(t *testing.T) {
		srv, st := gateTestServer(t)
		if err := st.SaveSystemSettings(ctx, map[string]string{
			adminSettingsKeyActiveCampaignID:   "the-vault",
			adminSettingsKeyCampaignJoinLocked: "true",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveCharacter(ctx, store.Character{
			ID: "c1", CampaignID: "the-vault", OwnerID: "discord:99",
			SchemaVersion: "v1", Status: store.CharacterStatusApproved,
			CharacterData: json.RawMessage(`{"name":"Bram"}`),
		}); err != nil {
			t.Fatal(err)
		}

		if ok, reason, _ := srv.sessionGate(ctx, "the-vault", "discord:404"); ok || reason == "" {
			t.Errorf("newcomer while locked: ok=%v reason=%q, want refused", ok, reason)
		}
		if ok, _, err := srv.sessionGate(ctx, "the-vault", "discord:99"); err != nil || !ok {
			t.Errorf("returning player while locked: ok=%v err=%v, want admitted", ok, err)
		}
	})
}

func TestSessionInfoHandler(t *testing.T) {
	ctx := context.Background()
	srv, st := gateTestServer(t)

	// Nothing active.
	rec := httptest.NewRecorder()
	srv.SessionInfoHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/session", nil))
	var got sessionInfoDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.CampaignID != "" {
		t.Errorf("no active campaign: got %+v, want empty", got)
	}

	// Active campaign with a name and a password.
	if err := st.SaveSystemSettings(ctx, map[string]string{adminSettingsKeyActiveCampaignID: "the-vault"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCampaignMeta(ctx, "the-vault", "The Sunken Vault"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCampaignSettings(ctx, "the-vault", store.CampaignSettings{RoomPassword: "hunter2"}); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	srv.SessionInfoHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/session", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.CampaignID != "the-vault" || got.DisplayName != "The Sunken Vault" || !got.NeedsPassword {
		t.Errorf("active session = %+v, want the-vault / The Sunken Vault / needs password", got)
	}
	if got.JoinLocked {
		t.Error("JoinLocked = true, want false")
	}
}
