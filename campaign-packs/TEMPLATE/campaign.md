---
# ── campaign.md — the one required file in a pack ──────────────────────
# Copy this whole directory, rename it to your pack's slug (lowercase,
# hyphens, no spaces), and replace every placeholder below. Delete the
# example location/npc/encounter files or replace them with your own.
# Anything with a "#" in front of it is a comment and is ignored.
#
# Required:
id: template-pack            # must be non-empty and unique; conventionally
                             # the same as the directory name
title: "The Template Pack"   # quote any value containing a colon

# Recommended — all optional, but the DM AI uses them for grounding:
level_range: "1-3"           # a string, not a number range
tone:                        # free-form style tags; a handful is plenty
  - "starter"
  - "investigation"
  - "wilderness peril"

# Governance (see docs/authoring-campaign-packs.md and design doc §9). A
# pack that omits these gets the strictest safe defaults.
pvp_policy: pve_only         # pve_only | pvp_allowed
maturity_tier: standard      # a reference to a maturity-tiers/<id>.md file
image_maturity_tier: family_friendly
shared_knowledge: strict     # strict = NPCs only know what they've actually
                             # learned; the DM must not treat them as omniscient

# Session-zero safety constraints (design doc §9.3). Enforced as standing
# limits from the first session, not just table etiquette.
lines:                       # hard "never happens on screen or off"
  - "no depictions of harm to real-world children or animals"
veils:                       # "happens, but off screen / not described"
  - "off-screen: torture, executions of captives"

author: "Your name or handle (original content, SRD 5.1-legal only)"
content_warnings:
  - "combat violence"

# Optional: a chapters outline. Roughly one level's worth of content per
# chapter. Individual location/npc/encounter files opt in via their own
# `chapter:` field. Omit this whole block for a pack with no chapters —
# it's exactly as valid (campaign-packs/sable-ravine uses none).
chapters:
  - id: chapter-1
    title: "Arrival"
    level_range: "1-2"
    summary: "The party reaches the frontier settlement and learns why they were sent."
---

# The Template Pack

Replace this body with your adventure's **overview** — two or three
paragraphs a DM (human or AI) can read once and understand: where this
takes place, what's wrong, who wants the party involved and why, and what
"resolving it" roughly looks like. Write it as briefing, not prose
fiction — concrete facts the DM will reference mid-session, not
atmosphere.

## Hooks

- **The straightforward hook** — someone hires or asks the party
  directly. State who, what they offer, and what they actually want.
- **The personal hook** — a reason one party member specifically is
  already invested (a missing relative, an old debt, a rumor they've
  chased for years). Use alongside or instead of the first.

## Running this pack

Note anything a DM needs to know that isn't obvious from the individual
files: what the *real* problem is (vs. its visible symptoms), how
`shared_knowledge: strict` bites here, which threads are optional, and
what a party that rushes straight to the end will miss.
