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

func TestSharedKnowledgePolicy_IsValid(t *testing.T) {
	tests := []struct {
		name string
		s    SharedKnowledgePolicy
		want bool
	}{
		{"Unspecified_ReturnsFalse", SharedKnowledgeUnspecified, false},
		{"Strict_ReturnsTrue", SharedKnowledgeStrict, true},
		{"PartyOmniscient_ReturnsTrue", SharedKnowledgePartyOmniscient, true},
		{"UnrecognizedValue_ReturnsFalse", SharedKnowledgePolicy("split_party"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("SharedKnowledgePolicy(%q).IsValid() = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestCampaignPolicy_EffectiveSharedKnowledge(t *testing.T) {
	tests := []struct {
		name string
		p    CampaignPolicy
		want SharedKnowledgePolicy
	}{
		{"Unset_ResolvesToPartyOmniscient", CampaignPolicy{}, SharedKnowledgePartyOmniscient},
		{"Strict_StaysStrict", CampaignPolicy{SharedKnowledge: SharedKnowledgeStrict}, SharedKnowledgeStrict},
		{"PartyOmniscient_StaysPartyOmniscient", CampaignPolicy{SharedKnowledge: SharedKnowledgePartyOmniscient}, SharedKnowledgePartyOmniscient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.EffectiveSharedKnowledge(); got != tt.want {
				t.Errorf("EffectiveSharedKnowledge() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCampaignPolicy_LevelInRange(t *testing.T) {
	tests := []struct {
		name  string
		p     CampaignPolicy
		level int
		want  bool
	}{
		{"Unconfigured_AnyLevelInRange", CampaignPolicy{}, 20, true},
		{"UnknownLevelZero_AlwaysInRange", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 0, true},
		{"WithinBothBounds", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 5, true},
		{"BelowMinLevel", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 2, false},
		{"AboveMaxLevel", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 9, false},
		{"AtMinLevel_Inclusive", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 3, true},
		{"AtMaxLevel_Inclusive", CampaignPolicy{MinLevel: 3, MaxLevel: 8}, 8, true},
		{"OnlyMinConfigured_AboveIsFine", CampaignPolicy{MinLevel: 3}, 20, true},
		{"OnlyMinConfigured_BelowRejected", CampaignPolicy{MinLevel: 3}, 1, false},
		{"OnlyMaxConfigured_BelowIsFine", CampaignPolicy{MaxLevel: 8}, 1, true},
		{"OnlyMaxConfigured_AboveRejected", CampaignPolicy{MaxLevel: 8}, 9, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.LevelInRange(tt.level); got != tt.want {
				t.Errorf("LevelInRange(%d) = %v, want %v", tt.level, got, tt.want)
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
