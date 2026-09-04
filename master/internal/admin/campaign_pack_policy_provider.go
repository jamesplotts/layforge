// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"context"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// CampaignPackPolicyProvider implements policy.Provider by resolving
// pvp_policy from a campaign's bound campaign pack (design doc §6.4) —
// campaign.md's own front matter. This is the thing
// campaign-packs/README.md names as blocked on real pack-loading
// ("pvp_policy... resolved from a flat per-campaign JSON file... rather
// than campaign.md front matter... a deliberate, documented interim
// scope... once Master actually loads campaign packs"): with LoadPack
// now real, a bound pack's own governance front matter can finally take
// effect.
//
// maturity_tier is deliberately NOT resolved here: design doc §6.5
// defines it as a reference to a separate maturity_tiers/<id>.md file
// (id, display_name, rank front matter; body is the actual prompt-
// constraint text) — a distinct, not-yet-built content loader, not a
// prompt string campaign.md carries directly. Copying campaign.md's raw
// maturity_tier value (e.g. "standard") into
// policy.CampaignPolicy.MaturityTierPrompt would inject that literal
// word as if it were constraint text, which is wrong, not just
// incomplete — so this provider leaves MaturityTierPrompt unset and
// falls through to Fallback for it, the same as it does for a campaign
// with no pack bound at all.
type CampaignPackPolicyProvider struct {
	store    store.CampaignPackStore
	Fallback policy.Provider
}

var _ policy.Provider = (*CampaignPackPolicyProvider)(nil)

// NewCampaignPackPolicyProvider creates a CampaignPackPolicyProvider
// backed by s, falling back to fallback (which may be nil, meaning
// policy.Default()) for a campaign with no pack bound, or whose bound
// pack doesn't parse or carries no real pvp_policy.
func NewCampaignPackPolicyProvider(s store.CampaignPackStore, fallback policy.Provider) *CampaignPackPolicyProvider {
	return &CampaignPackPolicyProvider{store: s, Fallback: fallback}
}

// Policy implements policy.Provider.
func (p *CampaignPackPolicyProvider) Policy(ctx context.Context, campaignID string) (policy.CampaignPolicy, error) {
	fallback := func() (policy.CampaignPolicy, error) {
		if p.Fallback != nil {
			return p.Fallback.Policy(ctx, campaignID)
		}
		return policy.Default(), nil
	}

	binding, ok, err := p.store.GetCampaignPack(ctx, campaignID)
	if err != nil {
		return policy.CampaignPolicy{}, err
	}
	if !ok {
		return fallback()
	}
	pack, err := campaignpack.LoadPack(binding.PackDir)
	if err != nil {
		// A bound directory that no longer parses (moved, edited badly
		// since binding) shouldn't take the whole campaign's governance
		// down with it — fall back the same as "nothing bound."
		return fallback()
	}
	if !policy.PvPPolicy(pack.PvPPolicy).IsValid() {
		return fallback()
	}
	return policy.CampaignPolicy{PvPPolicy: policy.PvPPolicy(pack.PvPPolicy)}, nil
}
