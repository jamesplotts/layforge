# Layforge

A chat-based virtual TTRPG platform where an AI acts as Dungeon Master.

The system splits into a **Master** node — the only component holding LLM
provider credentials and authoritative game state — and any number of thin
**Slave** clients that render UI and carry a network connection, with no
rules engine or provider credentials of their own. Everything mechanically
consequential (dice, rules resolution, PvP, character validation, content
policy) is a gate enforced in code at the tool-call layer, not something
left to the model's discretion. See [`docs/design.md`](docs/design.md) for
the full design document — architecture, protocol, extensibility
interfaces, and governance model.

## Status

Multiple players can connect to the same campaign, chat, and get real
AI-narrated responses: the `system.connect` handshake, then Master routes
messages between everyone connected — `safety.flag` broadcasts to the
whole table (design doc §9.2), and `narrative.player_input` renders
through an LLM into `narrative.player_bubble` (§7's fast pass only, no
DM/NPC reaction pass yet). Everything exchanged is durably logged to
SQLite, with bidirectional history paging — join and see what's recent,
page back further for scrollback. The V1 web client is served by Master
itself by default, straight from disk so a table can restyle it without
touching Go or rebuilding anything (see [`master/web/README.md`](master/web/README.md)).
Master can now dial a real OpenCombatEngine gRPC sidecar
(`-system-engine-addr`) and calls it for real: uploading a character
(`character.upload`) gets mechanically parsed and validated by the engine
and persisted, with the engine's warnings sent back
(`character.validation_result`, design doc §9.4's mechanical half), and a
player can roll an authoritative check for a character they own
(`roll.check_request`), with the real outcome — including individual
dice, not just a total — broadcast to the whole table as
`roll.request`/`roll.result`, animated on a real WebGL d20 (three.js +
cannon-es physics) with a swappable community-skin system in the web
client. A player can also read back their own character's current data
and mechanical status, rendered as a read-only sheet generated directly
from the system engine's own JSON Schema — no D&D-specific fields
hardcoded into the UI — and apply a real effect (damage/heal) to it,
persisted server-side and reflected back in the sheet. A DM/NPC reaction
pass (§7's slow pass) now runs after the fast pass: an LLM narrates what
happens next and can call real tools — `resolve_check`, `apply_effect`,
`get_character_status` (design doc §8) — against the same System Engine
RPCs already wired for players, with every call broadcast to the table as
`tool.result` and the final reaction as `narrative.dm_prose`. The DM can
also start real structured turn order (`start_combat`) — rolling
initiative through the System Engine rather than trusting the model to
order it — broadcast to everyone as `turn.state`, and now actually
enforced on players too: once combat is active, a player's own roll or
effect on their character is rejected unless it's currently that
character's turn (`advance_turn`, design doc §9.3). An unconscious/dying
character isn't skipped — they get a turn that automatically rolls a
death saving throw instead (a real System Engine RPC, `StartTurn`,
newly added so OpenCombatEngine's own SRD death-save logic — previously
built but unreachable — is actually wired up), broadcast as a genuine
roll just like any other. The DM can also give a narrated monster/NPC a
real
mechanical presence on the fly — `create_npc` (after `get_character_schema`
so the model authors a document that actually matches the campaign's
schema, never a guessed shape) persists it the same way a player's own
character upload does, so it can then be referenced by `resolve_check`,
`apply_effect`, or `start_combat` like any other character. A campaign
can now also configure a real PvP policy — `pve_only`/`pvp_allowed`/
`pvp_with_consent` — that mechanically gates whether the DM can damage
one player's character on another's behalf, and a maturity-tier text
constraint injected into DM narration, both via a per-campaign JSON
config file (`-campaign-policies`); an unconfigured campaign gets the
strictest PvP setting by default, not an open one. Turns are now
mechanically tied to real rules too: landing a turn on any non-dead
character automatically rolls a death saving throw for one that's
unconscious/dying (a real System Engine RPC, `StartTurn`) — SRD's own
"deterministic bookkeeping, not something the DM has to remember," now
actually wired up rather than sitting unreachable in the engine. The DM
can also illustrate a scene — `generate_scene_image` calls a pluggable
image-gen provider (a self-hosted ComfyUI instance is the reference
implementation) and broadcasts the result to the table, now verified
live against a real running ComfyUI instance. A local-only admin/operator
settings panel now exists too — a second, `127.0.0.1`-only listener
serving a tabbed web UI (Campaign/Security/System) for changing PvP
policy, maturity-tier prompts, and room passwords live, or process-level
settings (LLM/System-Engine/ComfyUI endpoints, listen address) via a
self-triggered graceful restart. A
player's own roll still doesn't apply its own damage outside the DM
pass, there's no human review step on imported characters (that needs an
account/operator concept the admin panel doesn't cover yet), and no full markdown
campaign-pack directory tree (§6.4) — policy configuration today is a
flat JSON file, not `campaign.md` front matter. See
[`master/README.md`](master/README.md) for the full picture, including
exact roadmap gaps.

## Layout

```
master/            Master process (Go) — session orchestration, turn-order
                    state machine, authoritative dice, tool-use dispatch,
                    and (master/web/) the V1 chat/player client it serves
protocol/          AsyncAPI spec for the client-facing WebSocket protocol,
                    plus the System Engine gRPC/protobuf contract
campaign-packs/    Directory-based campaign content (markdown + YAML)
maturity-tiers/    Content-maturity tier definitions
docs/              Design document and supporting docs
```

## Related repos

- [`jamesplotts/opencombatengine`](https://github.com/jamesplotts/opencombatengine)
  — the D&D SRD-compatible system-engine reference implementation this
  harness plugs into over gRPC (see §6.1 and §12 of the design doc).

## License

MIT for code. Game-mechanics content generated by or shipped with this
project is intended to stay SRD-legal (see §6.4 and §12 of the design doc)
— it does not reproduce proprietary published material.
