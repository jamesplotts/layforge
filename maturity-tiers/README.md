# Maturity Tiers

Extensible content-maturity definitions, same markdown + YAML front-matter
pattern as campaign packs: `id`, `display_name`, `rank` in front matter,
prompt-constraint text injected into DM generation in the body.

`rank` lets Master sanity-check that an image-gen tier override isn't more
permissive than the text tier for a campaign. Tier files are host-authored
and trusted like any other host config — this is a prompting-level policy,
not a hard content filter. See [`docs/design.md`](../docs/design.md) §6.5.

Not yet written. Expected: `family_friendly.md`, `standard.md`, `mature.md`.
