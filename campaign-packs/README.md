# Campaign Packs

Directory-based campaign content: markdown + YAML front matter. Each pack
is `campaign.md` (title, level range, `pvp_policy`, `maturity_tier`,
`shared_knowledge`, lines/veils, content warnings) plus `locations/*.md`,
`npcs/*.md`, `encounters/*.md`, and a `state.json` for mutable session
state kept separate from static content.

Any pack committed here must be original, SRD-legal, tone-inspired-only
content — see [`docs/design.md`](../docs/design.md) §6.4 and §12, and
[`CLAUDE.md`](../CLAUDE.md).

No packs shipped yet, and Master doesn't load this directory at all yet
either. `pvp_policy` and `maturity_tier` — the two fields §9.1/§9.5's
governance gates actually need — are real and enforced today, but
resolved from a flat per-campaign JSON file (`-campaign-policies`, see
`master/internal/policy`) rather than `campaign.md` front matter here.
That's a deliberate, documented interim scope (see
`policy.JSONFileProvider`'s doc comment) — this directory's full
markdown tree is still the intended long-term source, once Master
actually loads campaign packs.
