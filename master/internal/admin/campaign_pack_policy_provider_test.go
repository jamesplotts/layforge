// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"context"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/admin"
	"github.com/jamesplotts/layforge/master/internal/policy"
)

func TestCampaignPackPolicyProvider_NoPackBound_FallsBackToFallback(t *testing.T) {
	s := newTestStore(t)
	fallback := fakePolicyProvider{policy: policy.CampaignPolicy{PvPPolicy: policy.PvPPolicyAllowed}}
	p := admin.NewCampaignPackPolicyProvider(s, fallback)

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
	p := admin.NewCampaignPackPolicyProvider(s, fallback)

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
	p := admin.NewCampaignPackPolicyProvider(s, fallback)

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
	p := admin.NewCampaignPackPolicyProvider(s, nil)

	got, err := p.Policy(context.Background(), "campaign-never-bound")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != policy.PvPPolicyPveOnly {
		t.Errorf("PvPPolicy = %q, want %q (policy.Default())", got.PvPPolicy, policy.PvPPolicyPveOnly)
	}
}
