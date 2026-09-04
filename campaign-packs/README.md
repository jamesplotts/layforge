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

Originally live-playtested by hand-feeding this pack's content into the
DM's context as a human GM would, before Master could load it itself —
see the campaign's own git history for that session's notes. **Since
closed**: Master now really loads this directory. `internal/campaignpack`
parses `campaign.md`/`locations/*.md`/`npcs/*.md`/`encounters/*.md`
directly (this pack's exact real front matter, no hand-feeding); a host
binds a pack directory to a campaign via the admin panel's Campaign tab
(`PUT /api/campaigns/{id}/pack`), which validates by actually parsing
the directory first — a bad path is rejected outright, not silently
accepted. Once bound, the DM gets real `list_locations`/`travel_to`
tools gated against this pack's own real `connections` graph, plus
off-site possessions (`stash_item`/`retrieve_item`/`stash_currency`/
`retrieve_currency`, gated to the party's actual current location) and
land holdings (`claim_location`) — and now `list_npcs`/`list_encounters`
too, so the DM checks this pack's real, pre-authored NPCs and set-piece
encounters (Captain Orlen Vashti, the goblin-camp parley-or-raid
branch, the shrine's guardian) before improvising generic ones from
nothing. Real mounts/carts/wagons/ships too
(`list_vehicles`/`acquire_vehicle`/`stable_vehicle`/`take_vehicle`, plus
a player-facing `vehicle.import` protocol message independent of the
DM's own tool loop) — a vehicle is tracked by location the same way a
stash is, never as a character/creature record. `state.json`'s own
mutable fields
(`discovered_locations`, party location) are tracked in Master's SQLite
store instead of this file, which stays what it always was: the
starting shape, not something read or written at runtime.

`pvp_policy` and `maturity_tier` — the two fields §9.1/§9.5's governance
gates need — now really do resolve from this file's own front matter
(`pve_only`/`standard` above) when a pack is bound and the admin panel
hasn't set an explicit override, closing the interim scope this section
used to describe. See `internal/admin/campaign_pack_policy_provider.go`.
`maturity_tier` resolves independently of `pvp_policy` and only when a
`-maturity-tiers-dir` is configured (see
[`../maturity-tiers/README.md`](../maturity-tiers/README.md)) — a
campaign pack's own real `pvp_policy` still applies even when no tier
registry is set up at all.
