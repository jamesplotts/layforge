// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package registry_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/registry"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// fakeRegistryServer is a minimal, controllable stand-in for the real
// registry/ service's own HTTP API — records every call it receives so
// tests can assert on exactly what HeartbeatLoop sent, and lets a test
// force a 404 on demand (simulating the registry losing a listing —
// TTL expiry or a restart) to exercise the re-register path.
type fakeRegistryServer struct {
	mu            sync.Mutex
	registerCalls int
	heartbeats    []map[string]any
	deletes       []string
	forceNotFound bool
	nextID        int
}

func newFakeRegistryServer(t *testing.T) (*httptest.Server, *fakeRegistryServer) {
	t.Helper()
	f := &fakeRegistryServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/listings", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.registerCalls++
		f.nextID++
		id := f.nextID
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": intToID(id), "token": "token-" + intToID(id)})
	})
	mux.HandleFunc("PUT /api/v1/listings/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.forceNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		body["_id"] = r.PathValue("id")
		f.heartbeats = append(f.heartbeats, body)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /api/v1/listings/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.deletes = append(f.deletes, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func intToID(i int) string {
	return "listing-" + string(rune('0'+i))
}

func (f *fakeRegistryServer) snapshot() (registerCalls int, heartbeats []map[string]any, deletes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerCalls, append([]map[string]any(nil), f.heartbeats...), append([]string(nil), f.deletes...)
}

func newTestStore(t *testing.T) *store.SQLiteEventStore {
	t.Helper()
	s, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHeartbeatLoop_RunOnce_UnlistedCampaign_NeverCallsRegistry(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{RegistryListed: false}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx)

	registerCalls, _, _ := fake.snapshot()
	if registerCalls != 0 {
		t.Errorf("registerCalls = %d, want 0 for a campaign that never opted in", registerCalls)
	}
}

func TestHeartbeatLoop_RunOnce_ListedWithoutJoinAddress_NeverCallsRegistry(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{RegistryListed: true, JoinAddress: ""}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx)

	registerCalls, _, _ := fake.snapshot()
	if registerCalls != 0 {
		t.Errorf("registerCalls = %d, want 0 — RegistryListed without a JoinAddress must not be enough to list", registerCalls)
	}
}

func TestHeartbeatLoop_RunOnce_ListedCampaign_RegistersWithCorrectFields(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignMeta(ctx, "campaign-1", "The Sable Ravine"); err != nil {
		t.Fatalf("SaveCampaignMeta() error = %v", err)
	}
	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{
		RegistryListed: true,
		JoinAddress:    "wss://myhost.example.com/ws",
		MinLevel:       1,
		MaxLevel:       3,
		MaxPlayers:     5,
		RoomPassword:   "hunter2",
	}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}
	now := time.Now().UTC()
	if err := s.SaveCharacter(ctx, store.Character{
		ID: "char-1", CampaignID: "campaign-1", OwnerID: "player-a",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}
	if err := s.SaveCharacter(ctx, store.Character{
		// An NPC (OwnerID "master") must not count toward PlayersJoined.
		ID: "npc-1", CampaignID: "campaign-1", OwnerID: "master",
		Status: store.CharacterStatusApproved, SchemaVersion: "v1",
		CharacterData: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCharacter() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx)

	registerCalls, _, _ := fake.snapshot()
	if registerCalls != 1 {
		t.Fatalf("registerCalls = %d, want 1", registerCalls)
	}

	// A second RunOnce should heartbeat the already-registered listing,
	// not register a second one.
	loop.RunOnce(ctx)
	registerCalls, heartbeats, _ := fake.snapshot()
	if registerCalls != 1 {
		t.Errorf("registerCalls after second RunOnce = %d, want still 1", registerCalls)
	}
	if len(heartbeats) != 1 {
		t.Fatalf("heartbeats = %+v, want exactly 1", heartbeats)
	}
	hb := heartbeats[0]
	if hb["adventure_name"] != "The Sable Ravine" {
		t.Errorf("adventure_name = %v, want %q (from campaign meta display name)", hb["adventure_name"], "The Sable Ravine")
	}
	if hb["join_url"] != "wss://myhost.example.com/ws" {
		t.Errorf("join_url = %v, want the configured JoinAddress", hb["join_url"])
	}
	if v, _ := hb["min_level"].(float64); int(v) != 1 {
		t.Errorf("min_level = %v, want 1", hb["min_level"])
	}
	if v, _ := hb["max_level"].(float64); int(v) != 3 {
		t.Errorf("max_level = %v, want 3", hb["max_level"])
	}
	if v, _ := hb["player_slots"].(float64); int(v) != 5 {
		t.Errorf("player_slots = %v, want 5", hb["player_slots"])
	}
	if v, _ := hb["players_joined"].(float64); int(v) != 1 {
		t.Errorf("players_joined = %v, want 1 (the NPC must not count)", hb["players_joined"])
	}
	if hb["password_protected"] != true {
		t.Errorf("password_protected = %v, want true (a room password is set)", hb["password_protected"])
	}
	if hb["campaign_id"] != "campaign-1" {
		t.Errorf("campaign_id = %v, want campaign-1", hb["campaign_id"])
	}
}

func TestHeartbeatLoop_RunOnce_HeartbeatNotFound_ReregistersFresh(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{
		RegistryListed: true, JoinAddress: "wss://myhost.example.com/ws",
	}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx) // registers once

	fake.mu.Lock()
	fake.forceNotFound = true
	fake.mu.Unlock()
	loop.RunOnce(ctx) // heartbeat gets 404

	fake.mu.Lock()
	fake.forceNotFound = false
	fake.mu.Unlock()
	loop.RunOnce(ctx) // should register again, not error out forever

	registerCalls, _, _ := fake.snapshot()
	if registerCalls != 2 {
		t.Errorf("registerCalls = %d, want 2 (initial + re-register after a 404)", registerCalls)
	}
}

func TestHeartbeatLoop_RunOnce_OptOut_Deregisters(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	settings := store.CampaignSettings{RegistryListed: true, JoinAddress: "wss://myhost.example.com/ws"}
	if err := s.SaveCampaignSettings(ctx, "campaign-1", settings); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx) // registers

	settings.RegistryListed = false
	if err := s.SaveCampaignSettings(ctx, "campaign-1", settings); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}
	loop.RunOnce(ctx) // should deregister now

	_, _, deletes := fake.snapshot()
	if len(deletes) != 1 {
		t.Errorf("deletes = %v, want exactly 1 after opting out", deletes)
	}

	// A third RunOnce with the campaign still opted out must not try to
	// deregister again (nothing left to track).
	loop.RunOnce(ctx)
	_, _, deletes = fake.snapshot()
	if len(deletes) != 1 {
		t.Errorf("deletes after a further RunOnce = %v, want still 1 (no repeat deregister)", deletes)
	}
}

func TestHeartbeatLoop_RunOnce_ArchivedCampaign_NeverCallsRegistry(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveCampaignSettings(ctx, "campaign-1", store.CampaignSettings{
		RegistryListed: true, JoinAddress: "wss://myhost.example.com/ws",
	}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}
	if err := s.SetCampaignArchived(ctx, "campaign-1", true); err != nil {
		t.Fatalf("SetCampaignArchived() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx)

	registerCalls, _, _ := fake.snapshot()
	if registerCalls != 0 {
		t.Errorf("registerCalls = %d, want 0 — an archived campaign must never be published", registerCalls)
	}
}

func TestHeartbeatLoop_RunOnce_NoCampaignPack_FallsBackToDisplayNameThenCampaignID(t *testing.T) {
	ts, fake := newFakeRegistryServer(t)
	s := newTestStore(t)
	ctx := context.Background()

	// No SaveCampaignMeta call at all — DisplayName stays "".
	if err := s.SaveCampaignSettings(ctx, "unnamed-campaign", store.CampaignSettings{
		RegistryListed: true, JoinAddress: "wss://myhost.example.com/ws",
	}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	loop := registry.NewHeartbeatLoop(registry.NewClient(ts.URL), s, s, s, testLogger())
	loop.RunOnce(ctx) // registers
	loop.RunOnce(ctx) // heartbeats — this is the call whose body we inspect

	_, heartbeats, _ := fake.snapshot()
	if len(heartbeats) != 1 || heartbeats[0]["adventure_name"] != "unnamed-campaign" {
		t.Errorf("heartbeats = %+v, want adventure_name = %q (falls back to the raw campaign_id)", heartbeats, "unnamed-campaign")
	}
}
