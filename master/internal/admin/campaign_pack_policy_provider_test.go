// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/maturitytiers"
	"github.com/jamesplotts/layforge/master/internal/policy"
)

func TestCampaignPackPolicyProvider_NoPackBound_FallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyAllowed {
		t.Errorf("PvPPolicy = %q, want fallback's %q", got.PvPPolicy, policy.PvPPolicyAllowed)
	}
}

func TestCampaignPackPolicyProvider_BoundPack_ResolvesPvPPolicyFromCampaignMd(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// campaign-packs/sable-ravine/campaign.md's own front matter sets
	// pvp_policy: pve_only — the opposite of the fallback's pvp_allowed,
	// proving the bound pack actually won.
	if got.PvPPolicy != policy.PvPPolicyPveOnly {
		t.Errorf("PvPPolicy = %q, want %q (from campaign.md, not the fallback)", got.PvPPolicy, policy.PvPPolicyPveOnly)
	}
}

func TestCampaignPackPolicyProvider_BoundPackDoesNotParse_FallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", t.TempDir(), "broken"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyAllowed {
		t.Errorf("PvPPolicy = %q, want fallback's %q (a broken binding must not error out)", got.PvPPolicy, policy.PvPPolicyAllowed)
	}
}

func TestCampaignPackPolicyProvider_NilFallback_ResolvesToDefault(t *testing.T) {
	s := newTestStore(t)
	p := admin.NewCampaignPackPolicyProvider(s, nil, nil)

	got, err := p.Policy(context.Background(), "campaign-never-bound")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyPveOnly {
		t.Errorf("PvPPolicy = %q, want %q (policy.Default())", got.PvPPolicy, policy.PvPPolicyPveOnly)
	}
}

func TestCampaignPackPolicyProvider_BoundPack_ResolvesMaturityTierPromptFromTiersRegistry(t *testing.T) {
	s := newTestStore(t)
	tiers := map[string]maturitytiers.Tier{
		"standard": {ID: "standard", DisplayName: "Standard", Rank: 1, Prompt: "Real tier prompt text."},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, nil)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// campaign-packs/sable-ravine/campaign.md's own front matter sets
	// maturity_tier: standard.
	if got.MaturityTierPrompt != "Real tier prompt text." {
		t.Errorf("MaturityTierPrompt = %q, want the resolved tier's real prompt text", got.MaturityTierPrompt)
	}
}

func TestCampaignPackPolicyProvider_TierNotInRegistry_MaturityTierPromptFallsBack_ButPvPPolicyStillResolves(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{MaturityTierPrompt: "fallback prompt"}}
	// "standard" (what campaign.md references) is deliberately absent.
	tiers := map[string]maturitytiers.Tier{
		"mature": {ID: "mature", Prompt: "wrong tier, should never be used"},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.MaturityTierPrompt != "fallback prompt" {
		t.Errorf("MaturityTierPrompt = %q, want fallback's %q (unresolvable tier id must not silently pick a different tier)", got.MaturityTierPrompt, "fallback prompt")
	}
	// pvp_policy resolves independently of maturity_tier — an
	// unresolvable tier must not block the pack's own real pvp_policy.
	if got.PvPPolicy != policy.PvPPolicyPveOnly {
		t.Errorf("PvPPolicy = %q, want %q (must still resolve from campaign.md)", got.PvPPolicy, policy.PvPPolicyPveOnly)
	}
}

func TestCampaignPackPolicyProvider_NilTiers_MaturityTierPromptFallsBack(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{MaturityTierPrompt: "fallback prompt"}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.MaturityTierPrompt != "fallback prompt" {
		t.Errorf("MaturityTierPrompt = %q, want fallback's %q (no -maturity-tiers-dir configured)", got.MaturityTierPrompt, "fallback prompt")
	}
}

func TestCampaignPackPolicyProvider_BoundPack_ResolvesImageMaturityTierPromptWhenStricterThanText(t *testing.T) {
	s := newTestStore(t)
	tiers := map[string]maturitytiers.Tier{
		"standard":        {ID: "standard", Rank: 1, Prompt: "standard text prompt"},
		"family_friendly": {ID: "family_friendly", Rank: 0, Prompt: "family-friendly image prompt"},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, nil)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// campaign-packs/sable-ravine/campaign.md sets maturity_tier: standard
	// (rank 1) and image_maturity_tier: family_friendly (rank 0) — a
	// stricter image tier than text, the harmless direction design doc
	// §6.5 says is fine.
	if got.ImageMaturityTierPrompt != "family-friendly image prompt" {
		t.Errorf("ImageMaturityTierPrompt = %q, want the resolved stricter tier's own prompt", got.ImageMaturityTierPrompt)
	}
}

func TestCampaignPackPolicyProvider_ImageTierMorePermissiveThanText_RejectedFallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	writeFrontMatter(t, dir, "maturity_tier: standard\nimage_maturity_tier: mature\n")
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{ImageMaturityTierPrompt: "fallback image prompt"}}
	tiers := map[string]maturitytiers.Tier{
		"standard": {ID: "standard", Rank: 1, Prompt: "standard text prompt"},
		"mature":   {ID: "mature", Rank: 2, Prompt: "mature image prompt — must never be used here"},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", dir, "test-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// image_maturity_tier: mature (rank 2) is more permissive than
	// maturity_tier: standard (rank 1) — the one direction design doc
	// §6.5 says to guard against. Must be rejected, falling through to
	// Fallback exactly like an unresolvable tier id would.
	if got.ImageMaturityTierPrompt != "fallback image prompt" {
		t.Errorf("ImageMaturityTierPrompt = %q, want fallback's %q (a more-permissive image tier must be rejected)", got.ImageMaturityTierPrompt, "fallback image prompt")
	}
	if got.MaturityTierPrompt != "standard text prompt" {
		t.Errorf("MaturityTierPrompt = %q, want %q (must still resolve independently)", got.MaturityTierPrompt, "standard text prompt")
	}
}

func TestCampaignPackPolicyProvider_ImageTierSameRankAsText_Allowed(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	writeFrontMatter(t, dir, "maturity_tier: standard\nimage_maturity_tier: standard\n")
	tiers := map[string]maturitytiers.Tier{
		"standard": {ID: "standard", Rank: 1, Prompt: "standard prompt"},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, nil)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", dir, "test-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// The same tier for both text and image (a common, intentional
	// choice) is never a violation — equal rank is not "more permissive."
	if got.ImageMaturityTierPrompt != "standard prompt" {
		t.Errorf("ImageMaturityTierPrompt = %q, want %q (equal rank must be allowed)", got.ImageMaturityTierPrompt, "standard prompt")
	}
}

func TestCampaignPackPolicyProvider_ImageTierWithUnresolvableTextTier_AppliesUnclamped(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	writeFrontMatter(t, dir, "maturity_tier: does-not-exist\nimage_maturity_tier: mature\n")
	tiers := map[string]maturitytiers.Tier{
		"mature": {ID: "mature", Rank: 2, Prompt: "mature image prompt"},
	}
	p := admin.NewCampaignPackPolicyProvider(s, tiers, nil)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", dir, "test-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// No text tier resolved at all means there's nothing to sanity-check
	// the image tier against — it applies unclamped, the same
	// "independent resolution" reasoning maturity_tier/pvp_policy already
	// use elsewhere in this provider.
	if got.ImageMaturityTierPrompt != "mature image prompt" {
		t.Errorf("ImageMaturityTierPrompt = %q, want %q (nothing to compare against, so it applies as set)", got.ImageMaturityTierPrompt, "mature image prompt")
	}
}

func TestCampaignPackPolicyProvider_BoundPack_ResolvesSharedKnowledgeFromCampaignMd(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{SharedKnowledge: policy.SharedKnowledgePartyOmniscient}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// campaign-packs/sable-ravine/campaign.md's own front matter sets
	// shared_knowledge: strict — the opposite of the fallback's
	// party_omniscient, proving the bound pack actually won.
	if got.SharedKnowledge != policy.SharedKnowledgeStrict {
		t.Errorf("SharedKnowledge = %q, want %q (from campaign.md, not the fallback)", got.SharedKnowledge, policy.SharedKnowledgeStrict)
	}
}

func TestCampaignPackPolicyProvider_BoundPack_PreservesFallbackFieldsItDoesNotOverride(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{
		PvPPolicy:               policy.PvPPolicyAllowed,
		PvPConsent:              []string{"player-a"},
		ImageMaturityTierPrompt: "image tier text",
		PriceMultiplier:         1.5,
	}}
	p := admin.NewCampaignPackPolicyProvider(s, nil, fallback)

	if err := s.SaveCampaignPack(context.Background(), "campaign-1", sableRavinePackDir, "sable-ravine"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}

	got, err := p.Policy(context.Background(), "campaign-1")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	// pvp_policy is overridden by the pack (pve_only), but everything
	// else the pack doesn't carry an opinion on must survive from
	// Fallback rather than being reset to zero values.
	if got.PvPPolicy != policy.PvPPolicyPveOnly {
		t.Errorf("PvPPolicy = %q, want %q", got.PvPPolicy, policy.PvPPolicyPveOnly)
	}
	if len(got.PvPConsent) != 1 || got.PvPConsent[0] != "player-a" {
		t.Errorf("PvPConsent = %v, want [player-a] (preserved from fallback)", got.PvPConsent)
	}
	if got.ImageMaturityTierPrompt != "image tier text" {
		t.Errorf("ImageMaturityTierPrompt = %q, want %q (preserved from fallback)", got.ImageMaturityTierPrompt, "image tier text")
	}
	if got.PriceMultiplier != 1.5 {
		t.Errorf("PriceMultiplier = %v, want 1.5 (preserved from fallback)", got.PriceMultiplier)
	}
}

// writeFrontMatter writes a minimal, valid campaign.md into dir with
// extraFields appended verbatim into the YAML front matter — just
// enough for campaignpack.LoadPack to succeed, for tests that only care
// about one or two specific fields rather than a full real pack.
func writeFrontMatter(t *testing.T, dir, extraFields string) {
	t.Helper()
	content := "---\nid: test-pack\ntitle: Test Pack\n" + extraFields + "---\nOverview.\n"
	if err := os.WriteFile(filepath.Join(dir, "campaign.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(campaign.md) error = %v", err)
	}
}
