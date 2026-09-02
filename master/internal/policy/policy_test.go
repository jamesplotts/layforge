// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package policy

import "testing"

func TestPvPPolicy_IsValid(t *testing.T) {
	tests := []struct {
		name string
		p    PvPPolicy
		want bool
	}{
		{"Unspecified_ReturnsFalse", PvPPolicyUnspecified, false},
		{"PveOnly_ReturnsTrue", PvPPolicyPveOnly, true},
		{"Allowed_ReturnsTrue", PvPPolicyAllowed, true},
		{"WithConsent_ReturnsTrue", PvPPolicyWithConsent, true},
		{"UnrecognizedValue_ReturnsFalse", PvPPolicy("pvp_free_for_all"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.IsValid(); got != tt.want {
				t.Errorf("PvPPolicy(%q).IsValid() = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

func TestDefault_IsPveOnlyWithNoMaturityConstraint(t *testing.T) {
	got := Default()
	if got.PvPPolicy != PvPPolicyPveOnly {
		t.Errorf("Default().PvPPolicy = %q, want %q (the strictest setting)", got.PvPPolicy, PvPPolicyPveOnly)
	}
	if got.MaturityTierPrompt != "" {
		t.Errorf("Default().MaturityTierPrompt = %q, want empty (no constraint injected by default)", got.MaturityTierPrompt)
	}
	if len(got.PvPConsent) != 0 {
		t.Errorf("Default().PvPConsent = %v, want empty", got.PvPConsent)
	}
}

func TestCampaignPolicy_EffectiveImageMaturityTierPrompt(t *testing.T) {
	tests := []struct {
		name string
		p    CampaignPolicy
		want string
	}{
		{
			name: "ImageTierSet_ReturnsImageTier",
			p:    CampaignPolicy{MaturityTierPrompt: "text tier", ImageMaturityTierPrompt: "stricter image tier"},
			want: "stricter image tier",
		},
		{
			name: "ImageTierUnset_FallsBackToTextTier",
			p:    CampaignPolicy{MaturityTierPrompt: "text tier"},
			want: "text tier",
		},
		{
			name: "NeitherSet_ReturnsEmpty",
			p:    CampaignPolicy{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.EffectiveImageMaturityTierPrompt(); got != tt.want {
				t.Errorf("EffectiveImageMaturityTierPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}
