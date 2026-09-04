# Maturity Tiers

Extensible content-maturity definitions, same markdown + YAML front-matter
pattern as campaign packs: `id`, `display_name`, `rank` in front matter,
prompt-constraint text injected into DM generation in the body.

`rank` orders tiers from most to least restrictive (lower is more
restrictive) — the field a caller comparing a campaign's text tier against
an image-gen override would use to sanity-check the image tier isn't set
*more* permissive than the text tier (design doc §6.5: "the direction
worth guarding against; a stricter image tier than text is harmless").
That comparison itself isn't wired up anywhere yet — nothing in this
codebase resolves an *image* maturity tier from a tier id the way
`maturity_tier` resolves a text tier (see below); `image_maturity_tier_prompt`
is still operator-authored free text via the admin panel. A real, separate
follow-on, not silently dropped.

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
