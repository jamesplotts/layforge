// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package admin implements design doc §3.3's local-only admin/operator
// settings panel: a second HTTP listener, bound to 127.0.0.1 only, that
// lets Master's operator change Campaign/Security governance settings
// live and System-tab process settings via a restart. See Server's doc
// comment for the HTTP surface, and PolicyProvider/AuthProvider for how
// this package layers over — without replacing — the existing
// JSON-file-loaded providers in package policy/package auth.
package admin

import (
	"context"

	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// PolicyProvider implements policy.Provider by reading a campaign's
// governance settings live from store.AdminSettingsStore (design doc
// §3.3) — no in-memory cache, since SQLite already serializes concurrent
// access and a settings change should be visible on the very next call,
// not after some cache TTL. A campaign with no admin-saved row falls
// back to Fallback (typically the JSON-file-loaded
// policy.JSONFileProvider main.go already constructs from
// -campaign-policies, or nil), so nothing configured before this package
// existed stops working.
type PolicyProvider struct {
	store    store.AdminSettingsStore
	Fallback policy.Provider
}

var _ policy.Provider = (*PolicyProvider)(nil)

// NewPolicyProvider creates a PolicyProvider backed by s, falling back to
// fallback (which may be nil, meaning policy.Default()) for a campaign
// with no admin-saved settings.
func NewPolicyProvider(s store.AdminSettingsStore, fallback policy.Provider) *PolicyProvider {
	return &PolicyProvider{store: s, Fallback: fallback}
}

// Policy implements policy.Provider.
func (p *PolicyProvider) Policy(ctx context.Context, campaignID string) (policy.CampaignPolicy, error) {
	settings, ok, err := p.store.GetCampaignSettings(ctx, campaignID)
	if err != nil {
		return policy.CampaignPolicy{}, err
	}
	// A row exists but the admin panel never actually set a PvP policy on
	// it (e.g. only the room password half of the row was saved via the
	// Security tab) — treat that the same as no row at all, rather than
	// resolving to PvPPolicyUnspecified, which policy.CampaignPolicy never
	// represents as a real value (see policy.PvPPolicy.IsValid's doc
	// comment).
	if !ok || settings.PvPPolicy == "" {
		if p.Fallback != nil {
			return p.Fallback.Policy(ctx, campaignID)
		}
		return policy.Default(), nil
	}
	return policy.CampaignPolicy{
		PvPPolicy:               policy.PvPPolicy(settings.PvPPolicy),
		PvPConsent:              settings.PvPConsent,
		MaturityTierPrompt:      settings.MaturityTierPrompt,
		ImageMaturityTierPrompt: settings.ImageMaturityTierPrompt,
	}, nil
}
