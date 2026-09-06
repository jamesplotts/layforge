// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package registry_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/registry"
)

func testListing() registry.Listing {
	return registry.Listing{
		AdventureName:     "The Sable Ravine",
		MinLevel:          1,
		MaxLevel:          3,
		PlayersJoined:     2,
		PlayerSlots:       5,
		PasswordProtected: true,
		JoinURL:           "wss://example.com/ws",
		CampaignID:        "sable-ravine",
	}
}

func TestClient_Register_PostsFieldsAndReturnsIDAndToken(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "listing-1", "token": "secret-token"})
	}))
	defer ts.Close()

	client := registry.NewClient(ts.URL)
	id, token, err := client.Register(context.Background(), testListing())
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if id != "listing-1" || token != "secret-token" {
		t.Errorf("Register() = (%q, %q), want (listing-1, secret-token)", id, token)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/listings" {
		t.Errorf("request = %s %s, want POST /api/v1/listings", gotMethod, gotPath)
	}
	if gotBody["adventure_name"] != "The Sable Ravine" || gotBody["campaign_id"] != "sable-ravine" {
		t.Errorf("request body = %+v, missing expected fields", gotBody)
	}
}

func TestClient_Register_ServerError_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := registry.NewClient(ts.URL)
	if _, _, err := client.Register(context.Background(), testListing()); err == nil {
		t.Error("Register() error = nil, want an error for a 500 response")
	}
}

func TestClient_Heartbeat_PutsFieldsAndToken(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := registry.NewClient(ts.URL)
	if err := client.Heartbeat(context.Background(), "listing-1", "secret-token", testListing()); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/listings/listing-1" {
		t.Errorf("request = %s %s, want PUT /api/v1/listings/listing-1", gotMethod, gotPath)
	}
	if gotBody["token"] != "secret-token" {
		t.Errorf("request body token = %v, want secret-token", gotBody["token"])
	}
}

func TestClient_Heartbeat_404_ReturnsErrNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := registry.NewClient(ts.URL)
	err := client.Heartbeat(context.Background(), "does-not-exist", "token", testListing())
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Heartbeat() error = %v, want ErrNotFound", err)
	}
}

func TestClient_Deregister_DeletesWithToken(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	client := registry.NewClient(ts.URL)
	if err := client.Deregister(context.Background(), "listing-1", "secret-token"); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/listings/listing-1" {
		t.Errorf("request = %s %s, want DELETE /api/v1/listings/listing-1", gotMethod, gotPath)
	}
	if gotBody["token"] != "secret-token" {
		t.Errorf("request body token = %v, want secret-token", gotBody["token"])
	}
}
