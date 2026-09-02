# Campaign Packs

Directory-based campaign content: markdown + YAML front matter. Each pack
is `campaign.md` (title, level range, `pvp_policy`, `maturity_tier`,
`shared_knowledge`, lines/veils, content warnings) plus `locations/*.md`,
`npcs/*.md`, `encounters/*.md`, and a `state.json` for mutable session
state kept separate from static content.

Any pack committed here must be original, SRD-legal, tone-inspired-only
content — see [`docs/design.md`](../docs/design.md) §6.4 and §12, and
[`CLAUDE.md`](../CLAUDE.md).

## `sable-ravine/`

The first pack committed here: a short, original level 1–3 frontier
adventure (a lonely border keep, a monster-riddled ravine, several small
factions that turn out to be symptoms of one real problem rather than the
problem itself) — tone-inspired by classic low-level "wilderness
keep + dungeon-crawl" adventures, reusing none of any specific published
module's names, maps, NPCs, or text. Start at `sable-ravine/campaign.md`.

Live-playtested against a real running Master (real LLM narration, real
System Engine dice/combat) by hand-feeding this pack's content into the
DM's context as a human GM would — see the campaign's own git history
for that session's notes. That's necessarily how it was tested today:
Master doesn't parse or load this directory yet (see below), so nothing
here is validated by any automated ingestion, only by actually playing
it.

Master doesn't load this directory at all yet. `pvp_policy` and
`maturity_tier` — the two fields §9.1/§9.5's governance gates actually
need — are real and enforced today, but resolved from a flat
per-campaign JSON file (`-campaign-policies`, see `master/internal/policy`)
rather than `campaign.md` front matter here. That's a deliberate,
documented interim scope (see `policy.JSONFileProvider`'s doc comment) —
this directory's full markdown tree is still the intended long-term
source, once Master actually loads campaign packs.
