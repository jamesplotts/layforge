// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// fakeAuthProvider is a minimal auth.Provider fallback for exercising
// AuthProvider's fallback path without depending on
// auth.RoomPasswordProvider's own construction.
type fakeAuthProvider struct {
	ok     bool
	reason string
}

func (f fakeAuthProvider) Authorize(context.Context, string, string) (bool, string, error) {
	return f.ok, f.reason, nil
}

func TestAuthProvider_Authorize_NoStoredSettings_FallsBackToFallback(t *testing.T) {
	fallback := fakeAuthProvider{ok: false, reason: "fallback says no"}
	p := admin.NewAuthProvider(newTestStore(t), fallback)

	ok, reason, err := p.Authorize(context.Background(), "unconfigured-campaign", "whatever")
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if ok || reason != "fallback says no" {
		t.Errorf("Authorize() = (%v, %q), want fallback's (false, %q)", ok, reason, "fallback says no")
	}
}

func TestAuthProvider_Authorize_NoStoredSettingsOrFallback_ReturnsOpen(t *testing.T) {
	p := admin.NewAuthProvider(newTestStore(t), nil)

	ok, _, err := p.Authorize(context.Background(), "unconfigured-campaign", "")
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !ok {
		t.Error("Authorize() ok = false, want true (open, no provider configured at all)")
	}
}

func TestAuthProvider_Authorize_StoredPassword_CorrectToken_Authorized(t *testing.T) {
	s := newTestStore(t)
	p := admin.NewAuthProvider(s, nil)
	if err := s.SaveCampaignSettings(context.Background(), "campaign-1", store.CampaignSettings{RoomPassword: "hunter2"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	ok, _, err := p.Authorize(context.Background(), "campaign-1", "hunter2")
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !ok {
		t.Error("Authorize() ok = false, want true for the correct password")
	}
}

func TestAuthProvider_Authorize_StoredPassword_WrongToken_Rejected(t *testing.T) {
	s := newTestStore(t)
	p := admin.NewAuthProvider(s, nil)
	if err := s.SaveCampaignSettings(context.Background(), "campaign-1", store.CampaignSettings{RoomPassword: "hunter2"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	ok, reason, err := p.Authorize(context.Background(), "campaign-1", "wrong")
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if ok {
		t.Error("Authorize() ok = true, want false for the wrong password")
	}
	if reason == "" {
		t.Error("Authorize() reason is empty, want a human-readable rejection reason")
	}
}

func TestAuthProvider_Authorize_StoredEmptyPassword_FallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakeAuthProvider{ok: false, reason: "fallback says no"}
	p := admin.NewAuthProvider(s, fallback)

	// A row that only ever went through the Campaign tab (PvP policy
	// only) has an empty RoomPassword — that should not mean "the
	// password is the empty string," it should fall back exactly like no
	// row at all.
	if err := s.SaveCampaignSettings(context.Background(), "campaign-1", store.CampaignSettings{PvPPolicy: "pve_only"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	ok, reason, err := p.Authorize(context.Background(), "campaign-1", "")
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if ok || reason != "fallback says no" {
		t.Errorf("Authorize() = (%v, %q), want fallback's (false, %q)", ok, reason, "fallback says no")
	}
}
