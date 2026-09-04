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

// SharedKnowledgePolicy is the Go translation of design doc §9.7's
// shared_knowledge enum (strict | party_omniscient). The zero value,
// SharedKnowledgeUnspecified, is never a real policy value — see
// IsValid and EffectiveSharedKnowledge, which is what an unspecified
// policy resolves to in practice.
type SharedKnowledgePolicy string

// Recognized shared-knowledge policy values (design doc §9.7).
const (
	SharedKnowledgeUnspecified SharedKnowledgePolicy = ""
	// SharedKnowledgeStrict enables real private/split-party narration
	// (package server's narrate_privately DM tool) — split-party or
	// private-perception moments are scoped to only the characters
	// actually present/involved, not broadcast to the whole table.
	SharedKnowledgeStrict SharedKnowledgePolicy = "strict"
	// SharedKnowledgePartyOmniscient broadcasts all narration to
	// everyone regardless of character presence — today's behavior
	// everywhere else in this codebase, and what an unconfigured
	// campaign gets (see EffectiveSharedKnowledge): private narration
	// never appears out of nowhere for a campaign that never asked for it.
	SharedKnowledgePartyOmniscient SharedKnowledgePolicy = "party_omniscient"
)

// IsValid reports whether s is one of the two recognized policy values.
// Deliberately returns false for SharedKnowledgeUnspecified.
func (s SharedKnowledgePolicy) IsValid() bool {
	switch s {
	case SharedKnowledgeStrict, SharedKnowledgePartyOmniscient:
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

	// ImageMaturityTierPrompt is the equivalent constraint for
	// generate_scene_image calls (design doc §6.3, §9.5) — passed as the
	// maturity_tier argument to package imagegen. Design doc §6.3: an
	// image tier "may be configured stricter than the text tier for a
	// campaign, but never more permissive by default." Rather than
	// building design doc §6.5's full ranked-tier comparison to enforce
	// that (out of scope for the same reason the flat JSON policy file
	// itself is — see JSONFileProvider's doc comment), this package
	// enforces it by simpler default-inheritance: ImageTierPrompt (see
	// EffectiveImageMaturityTierPrompt) falls back to MaturityTierPrompt
	// whenever this field is left empty, so an operator gets the *same*
	// constraint for images as for text unless they explicitly set a
	// stricter one — never a silently more permissive one.
	ImageMaturityTierPrompt string

	// PriceMultiplier is a host-set per-campaign economic adjustment
	// (design doc §9's campaign-pack-scoped governance shape) applied
	// multiplicatively on top of the system engine's authoritative base
	// item price (see package server's vendor tools) — real, operator-set
	// "world information" data, never invented by the DM model. The zero
	// value means "not configured"; see EffectivePriceMultiplier, which
	// resolves that to 1.0 (engine-listed prices, unadjusted) rather than
	// letting an unconfigured campaign silently become a free-item
	// economy.
	PriceMultiplier float64

	// SharedKnowledge gates whether narrate_privately (package server,
	// design doc §9.7) exists at all for this campaign — the zero value
	// means "not configured"; see EffectiveSharedKnowledge, which
	// resolves that to SharedKnowledgePartyOmniscient (no private-
	// narration capability), the same "unconfigured never silently
	// grants a new capability" reasoning PriceMultiplier's own doc
	// comment uses.
	SharedKnowledge SharedKnowledgePolicy
}

// EffectiveSharedKnowledge returns p.SharedKnowledge, or
// SharedKnowledgePartyOmniscient when it's unset (the zero value) — see
// SharedKnowledge's own doc comment for why unset must not silently
// enable private narration nobody configured.
func (p CampaignPolicy) EffectiveSharedKnowledge() SharedKnowledgePolicy {
	if p.SharedKnowledge == SharedKnowledgeUnspecified {
		return SharedKnowledgePartyOmniscient
	}
	return p.SharedKnowledge
}

// EffectivePriceMultiplier returns p.PriceMultiplier, or 1.0 when it's
// unset (the zero value) — see PriceMultiplier's own doc comment for why
// zero must not mean "everything costs nothing."
func (p CampaignPolicy) EffectivePriceMultiplier() float64 {
	if p.PriceMultiplier == 0 {
		return 1.0
	}
	return p.PriceMultiplier
}

// EffectiveImageMaturityTierPrompt returns the constraint text to pass
// into an generate_scene_image call: p.ImageMaturityTierPrompt if the
// operator set one, otherwise p.MaturityTierPrompt (design doc §6.3's
// "never more permissive by default" — see ImageMaturityTierPrompt's
// doc comment for why this default-inheritance approach, not a ranked
// comparison, is how this package satisfies that).
func (p CampaignPolicy) EffectiveImageMaturityTierPrompt() string {
	if p.ImageMaturityTierPrompt != "" {
		return p.ImageMaturityTierPrompt
	}
	return p.MaturityTierPrompt
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
