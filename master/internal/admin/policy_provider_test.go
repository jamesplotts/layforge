// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func newTestStore(t *testing.T) *store.SQLiteEventStore {
	t.Helper()
	s, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

// fakePolicyProvider is a minimal policy.Provider fallback for exercising
// PolicyProvider's fallback path without depending on
// policy.JSONFileProvider's own file-loading.
type fakePolicyProvider struct {
	policy policy.CampaignPolicy
}

func (f fakePolicyProvider) Policy(context.Context, string) (policy.CampaignPolicy, error) {
	return f.policy, nil
}

func TestPolicyProvider_Policy_NoStoredSettings_FallsBackToFallback(t *testing.T) {
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewPolicyProvider(newTestStore(t), fallback)

	got, err := p.Policy(context.Background(), "unconfigured-campaign")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyAllowed {
		t.Errorf("PvPPolicy = %q, want fallback's %q", got.PvPPolicy, policy.PvPPolicyAllowed)
	}
}

func TestPolicyProvider_Policy_NoStoredSettingsOrFallback_ReturnsDefault(t *testing.T) {
	p := admin.NewPolicyProvider(newTestStore(t), nil)

	got, err := p.Policy(context.Background(), "unconfigured-campaign")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.Default().PvPPolicy || got.MaturityTierPrompt != policy.Default().MaturityTierPrompt {
		t.Errorf("Policy() = %+v, want policy.Default() %+v", got, policy.Default())
	}
}

func TestPolicyProvider_Policy_StoredSettings_ReturnsThemOverFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyPveOnly}}
	p := admin.NewPolicyProvider(s, fallback)

	stored := store.CampaignSettings{
		PvPPolicy:               string(policy.PvPPolicyWithConsent),
		PvPConsent:              []string{"player-a"},
		MaturityTierPrompt:      "Keep it family friendly.",
		ImageMaturityTierPrompt: "No graphic violence.",
	}
	if err := s.SaveCampaignSettings(context.Background(), "campaign-1", stored); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyWithConsent {
		t.Errorf("PvPPolicy = %q, want stored %q, not fallback", got.PvPPolicy, policy.PvPPolicyWithConsent)
	}
	if len(got.PvPConsent) != 1 || got.PvPConsent[0] != "player-a" {
		t.Errorf("PvPConsent = %v, want [player-a]", got.PvPConsent)
	}
	if got.MaturityTierPrompt != stored.MaturityTierPrompt {
		t.Errorf("MaturityTierPrompt = %q, want %q", got.MaturityTierPrompt, stored.MaturityTierPrompt)
	}
}

func TestPolicyProvider_Policy_RowExistsWithoutPvPPolicySet_FallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewPolicyProvider(s, fallback)

	// A row that only ever went through the Security tab (room password
	// only) has an empty PvPPolicy — that should not resolve to
	// PvPPolicyUnspecified, it should fall back exactly like no row at
	// all.
	if err := s.SaveCampaignSettings(context.Background(), "campaign-1", store.CampaignSettings{RoomPassword: "hunter2"}); err != nil {
		t.Fatalf("SaveCampaignSettings() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyAllowed {
		t.Errorf("PvPPolicy = %q, want fallback's %q", got.PvPPolicy, policy.PvPPolicyAllowed)
	}
}
