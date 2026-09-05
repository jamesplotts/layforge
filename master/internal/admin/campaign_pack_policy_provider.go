// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"context"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/maturitytiers"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// CampaignPackPolicyProvider implements policy.Provider by resolving
// pvp_policy, maturity_tier, image_maturity_tier, and shared_knowledge
// from a campaign's bound campaign pack (design doc §6.4) — campaign.md's
// own front matter. pvp_policy is the
// thing campaign-packs/README.md used to name as blocked on real
// pack-loading ("resolved from a flat per-campaign JSON file... rather
// than campaign.md front matter... a deliberate, documented interim
// scope... once Master actually loads campaign packs"): with LoadPack
// now real, a bound pack's own governance front matter can finally take
// effect.
//
// maturity_tier/image_maturity_tier resolve the same way, but only
// through tiers: design doc §6.5 defines each as a *reference* to a
// separate maturity-tiers/<id>.md file (id, display_name, rank front
// matter; body is the actual prompt-constraint text), not prompt text
// campaign.md carries directly — copying the raw tier id (e.g.
// "standard") into policy.CampaignPolicy.MaturityTierPrompt would
// inject that literal word as if it were constraint text, which is
// wrong, not just incomplete. tiers is the loaded registry
// (package maturitytiers) a caller resolves that reference against; nil
// or a missing id in it means the field simply doesn't resolve here
// (falls through to Fallback for that field), the same as a campaign
// with no pack bound at all. image_maturity_tier additionally gets
// maturity-tiers/README.md's own named follow-on: Policy rejects (falls
// through to Fallback, same as unresolvable) an image tier whose Rank is
// higher — more permissive — than the resolved text tier's, since design
// doc §6.5 only ever wants to guard against that one direction.
//
// shared_knowledge resolves the same direct way pvp_policy does — no
// separate content-file reference the way the tier fields need (design
// doc §9.7).
type CampaignPackPolicyProvider struct {
	store    store.CampaignPackStore
	tiers    map[string]maturitytiers.Tier
	Fallback policy.Provider
}

var _ policy.Provider = (*CampaignPackPolicyProvider)(nil)

// NewCampaignPackPolicyProvider creates a CampaignPackPolicyProvider
// backed by s, resolving maturity_tier references against tiers (may be
// nil, meaning maturity_tier never resolves — no -maturity-tiers-dir
// configured), falling back to fallback (which may be nil, meaning
// policy.Default()) for anything it can't resolve from a bound pack.
func NewCampaignPackPolicyProvider(s store.CampaignPackStore, tiers map[string]maturitytiers.Tier, fallback policy.Provider) *CampaignPackPolicyProvider {
	return &CampaignPackPolicyProvider{store: s, tiers: tiers, Fallback: fallback}
}

// Policy implements policy.Provider. pvp_policy and maturity_tier
// resolve independently of each other: a pack with a valid pvp_policy
// but an unresolvable maturity_tier (no tiers configured, or an id not
// in the registry) still gets its pvp_policy applied, with
// MaturityTierPrompt (and every other field — PvPConsent,
// ImageMaturityTierPrompt, PriceMultiplier) carried through from
// Fallback rather than discarded.
func (p *CampaignPackPolicyProvider) Policy(ctx context.Context, campaignID string) (policy.CampaignPolicy, error) {
	fallbackPolicy := func() (policy.CampaignPolicy, error) {
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
		return fallbackPolicy()
	}
	pack, err := campaignpack.LoadPack(binding.PackDir)
	if err != nil {
		// A bound directory that no longer parses (moved, edited badly
		// since binding) shouldn't take the whole campaign's governance
		// down with it — fall back the same as "nothing bound."
		return fallbackPolicy()
	}

	result, err := fallbackPolicy()
	if err != nil {
		return policy.CampaignPolicy{}, err
	}
	if policy.PvPPolicy(pack.PvPPolicy).IsValid() {
		result.PvPPolicy = policy.PvPPolicy(pack.PvPPolicy)
	}
	textTier, textResolved := p.tiers[pack.MaturityTier]
	if textResolved {
		result.MaturityTierPrompt = textTier.Prompt
	}
	// ImageMaturityTier resolves the same way, but with the rank
	// sanity-check maturity-tiers/README.md names as a real follow-on
	// (design doc §6.5: "the direction worth guarding against; a
	// stricter image tier than text is harmless"). Rank is lower-is-
	// more-restrictive, so a violation is imageTier.Rank > textTier.Rank
	// — strictly greater, so setting the *same* tier for both (a common,
	// intentional case) is never flagged. An image tier that can't be
	// compared (no text tier resolved at all) is applied unclamped —
	// there's nothing to sanity-check it against, same as pvp_policy/
	// maturity_tier resolving independently of each other elsewhere in
	// this method. A rejected image tier isn't reported anywhere (this
	// provider has no logger and every other silent-fallback case here —
	// an unparseable pack, an unresolvable tier id — already behaves the
	// same way); ImageMaturityTierPrompt simply stays whatever Fallback
	// already had, exactly like an unresolvable tier id does.
	if pack.ImageMaturityTier != "" {
		if imageTier, ok := p.tiers[pack.ImageMaturityTier]; ok {
			if !textResolved || imageTier.Rank <= textTier.Rank {
				result.ImageMaturityTierPrompt = imageTier.Prompt
			}
		}
	}
	if policy.SharedKnowledgePolicy(pack.SharedKnowledge).IsValid() {
		result.SharedKnowledge = policy.SharedKnowledgePolicy(pack.SharedKnowledge)
	}
	return result, nil
}
