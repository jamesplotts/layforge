# Campaign Packs

Directory-based campaign content: markdown + YAML front matter. Each pack
is `campaign.md` (title, level range, `pvp_policy`, `maturity_tier`,
`image_maturity_tier`, `shared_knowledge`, lines/veils, content
warnings) plus `locations/*.md`,
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

`pvp_policy`, `maturity_tier`, and `shared_knowledge` — the fields
§9.1/§9.5/§9.7's governance gates need — now really do resolve from this
file's own front matter (`pve_only`/`standard`/`strict` above) when a
pack is bound and the admin panel hasn't set an explicit override,
closing the interim scope this section used to describe. See
`internal/admin/campaign_pack_policy_provider.go`. `maturity_tier`
resolves independently of the other two and only when a
`-maturity-tiers-dir` is configured (see
[`../maturity-tiers/README.md`](../maturity-tiers/README.md)) — a
campaign pack's own real `pvp_policy`/`shared_knowledge` still apply
even when no tier registry is set up at all.

`image_maturity_tier` is a new, separate front-matter field (this
pack sets it to `family_friendly`, stricter than `maturity_tier:
standard`) — a reference into the same tier registry, resolved into
`generate_scene_image`'s own constraint text independently of the text
tier. `CampaignPackPolicyProvider.Policy` enforces the one direction
design doc §6.5 actually cares about: an image tier ranked *more
permissive* than the resolved text tier is rejected outright (falls
through the same way an unresolvable tier id does) — closing
`maturity-tiers/README.md`'s own previously-named follow-on. Equal
rank (a pack intentionally using the same tier for both) is always
allowed; a pack that sets no `image_maturity_tier` at all keeps the
existing text-tier-inherits-to-image behavior
(`policy.CampaignPolicy.EffectiveImageMaturityTierPrompt`) unchanged.
`shared_knowledge: strict`
(this pack's own setting) is what makes the DM's `narrate_privately`
tool (design doc §9.7, `internal/server/knowledge_scoping.go`) available
at all — `party_omniscient` (the default for a pack that omits the
field) leaves it unoffered, matching every campaign's behavior before
this field existed.
