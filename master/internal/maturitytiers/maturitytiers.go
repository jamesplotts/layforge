// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package maturitytiers loads a directory of maturity-tier definitions
// (design doc §6.5) — markdown + YAML front matter, one file per tier,
// e.g. maturity-tiers/standard.md — into structured data. Referenced
// per-campaign via a campaign pack's own campaign.md maturity_tier
// field (see package campaignpack), resolved to real prompt-constraint
// text by package admin's CampaignPackPolicyProvider.
//
// Tier files are host-authored and trusted exactly like any other host
// config or campaign pack — this package doesn't police tier content
// (design doc §6.5's own documented footgun). What ships in this repo's
// own maturity-tiers/ directory is a different matter: CLAUDE.md's
// content-maturity rule (no tiers aimed at eliciting explicit sexual
// content) applies to it like any other shipped content.
package maturitytiers

// Tier is one maturity tier's definition, loaded from a single
// maturity-tiers/*.md file.
type Tier struct {
	ID          string
	DisplayName string
	// Rank orders tiers from most to least restrictive — lower is more
	// restrictive. Lets a caller sanity-check that an image-gen tier
	// isn't set more permissive (a higher rank) than the campaign's text
	// tier (design doc §6.5: "the direction worth guarding against; a
	// stricter image tier than text is harmless"). Not enforced by this
	// package itself — package admin/server callers that resolve both a
	// text and an image tier are where that comparison belongs.
	Rank int
	// Prompt is the tier's markdown body — the actual prompt-constraint
	// text injected into DM generation (design doc §9.5).
	Prompt string
}
