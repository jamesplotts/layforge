# Maturity Tiers

Extensible content-maturity definitions, same markdown + YAML front-matter
pattern as campaign packs: `id`, `display_name`, `rank` in front matter,
prompt-constraint text injected into DM generation in the body.

`rank` orders tiers from most to least restrictive (lower is more
restrictive) — the field a caller comparing a campaign's text tier against
an image-gen override uses to sanity-check the image tier isn't set
*more* permissive than the text tier (design doc §6.5: "the direction
worth guarding against; a stricter image tier than text is harmless").

**Since closed**: a campaign pack's `campaign.md` can now set its own
`image_maturity_tier` front-matter field — a reference into this same
registry, resolved by `internal/admin/campaign_pack_policy_provider.go`
independently of (but sanity-checked against) `maturity_tier`. An image
tier ranked more permissive than the resolved text tier is rejected
(falls through the same way an unresolvable tier id does); equal rank —
using the same tier for both, a common, intentional choice — is always
allowed. A pack with no `image_maturity_tier` at all keeps the existing
text-tier-inherits-to-image fallback
(`policy.CampaignPolicy.EffectiveImageMaturityTierPrompt`) unchanged.
The admin panel's own `image_maturity_tier_prompt` free-text field is
untouched by any of this — it's a separate, directly-typed override, not
a tier-id reference.

Tier files are host-authored and trusted exactly like any other host
config or campaign pack — Master doesn't police tier content. This is a
prompting-level policy, not a hard technical content filter with a
guaranteed backstop.

**Since closed**: Master now really loads this directory.
`internal/maturitytiers` parses every `*.md` file directly inside it
(this repo's `family_friendly.md`/`standard.md`/`mature.md` are real,
shipped examples — see below) into `id -> Tier{DisplayName, Rank, Prompt}`.
A host points `-maturity-tiers-dir` at a directory of tier files (this one,
or their own); a campaign's bound pack's own `campaign.md` `maturity_tier`
field (design doc §6.4) is resolved against that registry by
`internal/admin/campaign_pack_policy_provider.go` — the *same* provider
that already resolves `pvp_policy` this way, now resolving both fields
independently (an unresolvable tier id doesn't block a real `pvp_policy`
from taking effect, and vice versa). No `-maturity-tiers-dir` configured
means `maturity_tier` references never resolve — a campaign pack's
`pvp_policy` still works, `maturity_tier_prompt` just stays whatever
`-campaign-policies`/the admin panel already had it as, same as before
this existed.

Verified live against a real sidecar + Master + `qwen3.8:27b`: with a
real pack bound (`campaign-packs/sable-ravine`, `maturity_tier: standard`)
and a scratch tier registry containing a `standard` tier with a
distinctive, testable marker instruction in its prompt text, the DM's
real narration opened with that exact marker, unprompted — proving the
resolved tier text actually reached generation, not just the resolution
logic in isolation.

The `image_maturity_tier` rank check above is covered by real-fixture
deterministic tests (`internal/admin/campaign_pack_policy_provider_test.go`
— using the actual shipped `sable-ravine` pack and real `Tier.Rank`
semantics for the allowed/rejected/equal-rank/no-text-tier cases, not
synthetic mocks), plus a live re-run of the same marker technique above
against a real running Master (a scratch pack setting both
`maturity_tier` and `image_maturity_tier`) confirming
`CampaignPackPolicyProvider.Policy` — the same function that resolves
and clamps `image_maturity_tier` — still runs correctly end to end
against real files in a real process. Not separately live-verified
through an actual `generate_scene_image`/ComfyUI call: the environment
available for this pass only had a personal, general-purpose ComfyUI
install unrelated to this project, and spinning up a real image-
generation job there just to re-observe a string substitution this
project's own prior ComfyUI pass already proved works wasn't worth the
risk or the GPU time — honestly noted as a live-verification gap rather
than silently skipped.

## Shipped example tiers

`family_friendly.md`, `standard.md`, `mature.md` — three real, original
tier definitions matching design doc §6.5's own example filenames,
written to stay unambiguously on the safe side of CLAUDE.md's
content-maturity rule at every rank, `mature` included: real permission
for darker themes and more visceral combat description, an explicit
line that sexual content is never permitted "regardless of this tier."
This project does not design or ship tiers aimed at eliciting explicit
sexual content, at any rank — see [`CLAUDE.md`](../CLAUDE.md) and
[`docs/design.md`](../docs/design.md) §6.5, §12.
