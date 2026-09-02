// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package policy implements design doc §9's campaign-pack-scoped
// governance settings — today, §9.1's PvP policy and §9.5's maturity-tier
// prompt constraint — as a pluggable per-campaign configuration surface,
// resolved by whoever needs a campaign's governance settings (see
// package server's campaignPolicy). See JSONFileProvider's doc comment
// for why this isn't (yet) design doc §6.4's full markdown campaign-pack
// directory tree.
package policy

import "context"

// PvPPolicy is the Go translation of design doc §9.1's pvp_policy enum
// (pve_only | pvp_allowed | pvp_with_consent). The zero value,
// PvPPolicyUnspecified, is never a real policy value — see IsValid, and
// Default, which is what an unspecified policy resolves to in practice.
type PvPPolicy string

// Recognized PvP policy values (design doc §9.1).
const (
	PvPPolicyUnspecified PvPPolicy = ""
	// PvPPolicyPveOnly blocks every hostile apply_effect targeting a
	// different player's character outright.
	PvPPolicyPveOnly PvPPolicy = "pve_only"
	// PvPPolicyAllowed permits it unconditionally.
	PvPPolicyAllowed PvPPolicy = "pvp_allowed"
	// PvPPolicyWithConsent permits it only against a character whose
	// owner has pre-declared consent — see CampaignPolicy.PvPConsent.
	PvPPolicyWithConsent PvPPolicy = "pvp_with_consent"
)

// IsValid reports whether p is one of the three recognized policy
// values. Deliberately returns false for PvPPolicyUnspecified — the Go
// translation of design doc §12's enum-sentinel pattern (see CLAUDE.md).
func (p PvPPolicy) IsValid() bool {
	switch p {
	case PvPPolicyPveOnly, PvPPolicyAllowed, PvPPolicyWithConsent:
		return true
	default:
		return false
	}
}

// CampaignPolicy is one campaign's governance settings (design doc §9).
type CampaignPolicy struct {
	// PvPPolicy gates whether a hostile apply_effect against a different
	// player's character is permitted to execute at all (design doc
	// §9.1) — enforced in dmApplyEffect (package server), never left to
	// the DM model's own judgment.
	PvPPolicy PvPPolicy

	// PvPConsent lists player sender_ids who have pre-declared
	// willingness to be targeted by hostile PvP actions (design doc
	// §9.1's "pre-session per-player opt-in flag") — consulted only when
	// PvPPolicy is PvPPolicyWithConsent. Design doc §9.1 also describes
	// an "in-the-moment Master confirmation" path; that needs a
	// request/response protocol round-trip and a way to address a
	// specific player that doesn't exist yet (the same
	// privileged-operator-concept gap CLAUDE.md's character-import veto
	// already documents), so only the pre-declared path is implemented.
	PvPConsent []string

	// MaturityTierPrompt, when non-empty, is appended as an additional
	// constraint to the DM's system prompt for both narrative passes
	// (design doc §9.5, §6.5) — operator-authored text, e.g. "keep
	// content suitable for all ages." Master neither authors nor ships
	// any tier content itself (CLAUDE.md: no maturity-tier content aimed
	// at eliciting explicit sexual content ships with this repo); an
	// empty string means no constraint is injected, not "family_friendly
	// by default" — see Default.
	MaturityTierPrompt string
}

// Default is the policy applied to a campaign a configured Provider
// doesn't know about, and to every campaign when no Provider is
// configured at all — PvPPolicyPveOnly, design doc §9.1's strictest
// setting, and no maturity-tier constraint. An operator who hasn't
// configured this setting for a campaign should never get PvP-enabled
// behavior by accident; the maturity side has no comparable safety
// concern (an unconfigured tier just means no additional constraint is
// injected, the same as this codebase's behavior before this package
// existed), so it stays neutral rather than picking a tier on the
// operator's behalf.
func Default() CampaignPolicy {
	return CampaignPolicy{PvPPolicy: PvPPolicyPveOnly}
}

// Provider resolves a campaign's governance policy (design doc §9).
type Provider interface {
	// Policy returns campaignID's policy. Implementations should prefer
	// returning Default() over a non-nil error for "campaign not
	// configured" — an error should mean resolution itself failed (e.g.
	// a future database-backed provider's connection dropped), since
	// callers treat any error as "fail closed to Default()" (see package
	// server's campaignPolicy) and a healthy "not configured" case
	// shouldn't be logged as a warning every time.
	Policy(ctx context.Context, campaignID string) (CampaignPolicy, error)
}
