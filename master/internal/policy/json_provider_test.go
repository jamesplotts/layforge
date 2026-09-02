// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package policy

import (
	"context"
	"reflect"
	"testing"
)

func TestJSONFileProvider_Policy_ConfiguredCampaign_ReturnsIt(t *testing.T) {
	want := CampaignPolicy{
		PvPPolicy:          PvPPolicyWithConsent,
		PvPConsent:         []string{"player-a", "player-b"},
		MaturityTierPrompt: "Keep content suitable for all ages.",
	}
	p := NewJSONFileProvider(map[string]CampaignPolicy{"my-campaign": want})

	got, err := p.Policy(context.Background(), "my-campaign")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Policy() = %+v, want %+v", got, want)
	}
}

func TestJSONFileProvider_Policy_UnconfiguredCampaign_ReturnsDefault(t *testing.T) {
	p := NewJSONFileProvider(map[string]CampaignPolicy{
		"other-campaign": {PvPPolicy: PvPPolicyAllowed},
	})

	got, err := p.Policy(context.Background(), "my-campaign")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("Policy() = %+v, want Default() = %+v", got, Default())
	}
}

func TestNewJSONFileProvider_DoesNotAliasCallersMap(t *testing.T) {
	source := map[string]CampaignPolicy{"my-campaign": {PvPPolicy: PvPPolicyAllowed}}
	p := NewJSONFileProvider(source)

	source["my-campaign"] = CampaignPolicy{PvPPolicy: PvPPolicyPveOnly}

	got, err := p.Policy(context.Background(), "my-campaign")
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if got.PvPPolicy != PvPPolicyAllowed {
		t.Errorf("Policy().PvPPolicy = %q after mutating the caller's map, want %q (construction should have copied it)", got.PvPPolicy, PvPPolicyAllowed)
	}
}
