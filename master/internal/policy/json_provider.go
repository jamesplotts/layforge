// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package policy

import "context"

// JSONFileProvider resolves each campaign's policy from a map built at
// startup from a flat JSON file the operator maintains directly — see
// main.go's loadCampaignPolicies, which mirrors
// auth.NewRoomPasswordProvider's loading pattern exactly (design doc
// §6.6's precedent for a per-campaign operator setting). This is
// deliberately not (yet) design doc §6.4's full markdown campaign-pack
// directory tree: campaign.md's own front matter is the intended
// long-term source for these same fields, once campaign packs themselves
// are loaded by Master, which they are not yet (see
// campaign-packs/README.md) — a documented interim scope, not the final
// shape.
type JSONFileProvider struct {
	// policies maps campaign_id to its configured policy. Read-only
	// after construction — see NewJSONFileProvider — so no locking is
	// needed for concurrent Policy calls.
	policies map[string]CampaignPolicy
}

var _ Provider = (*JSONFileProvider)(nil)

// NewJSONFileProvider creates a JSONFileProvider from a campaign_id ->
// policy mapping. policies is not retained by reference beyond
// construction; the caller's map may be freely mutated or discarded
// afterward.
func NewJSONFileProvider(policies map[string]CampaignPolicy) *JSONFileProvider {
	owned := make(map[string]CampaignPolicy, len(policies))
	for campaignID, p := range policies {
		owned[campaignID] = p
	}
	return &JSONFileProvider{policies: owned}
}

// Policy implements Provider. It never returns a non-nil error — a
// campaign absent from the configured file gets Default(), not a
// lookup failure.
func (p *JSONFileProvider) Policy(_ context.Context, campaignID string) (CampaignPolicy, error) {
	if configured, ok := p.policies[campaignID]; ok {
		return configured, nil
	}
	return Default(), nil
}
