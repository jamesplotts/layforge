# Maturity Tiers

Extensible content-maturity definitions, same markdown + YAML front-matter
pattern as campaign packs: `id`, `display_name`, `rank` in front matter,
prompt-constraint text injected into DM generation in the body.

`rank` lets Master sanity-check that an image-gen tier override isn't more
permissive than the text tier for a campaign. Tier files are host-authored
and trusted like any other host config — this is a prompting-level policy,
not a hard content filter. See [`docs/design.md`](../docs/design.md) §6.5.

Not yet written, and Master doesn't load this directory (or `rank`
sanity-checking) at all yet. The actual constraint-injection mechanism
§9.5 calls for is real and working today, verified live against a real
model on both narrative passes — but the constraint text itself is
supplied directly as `maturity_tier_prompt` in the same flat
`-campaign-policies` JSON file `campaign-packs/README.md` describes for
`pvp_policy`, operator-authored per campaign, not loaded from a tier file
here. This repo deliberately ships no tier content of its own (neither
here nor as example JSON) — see `CLAUDE.md`'s content-maturity rule.
