# Master

The Master process (Go). Session orchestration, turn-order state machine,
authoritative dice, campaign state store, narrative-transform pipeline,
tool-use dispatch, governance gates, and the WebSocket server clients
connect to.

## Status

Multiple clients can now connect to the same campaign and actually see
each other: `system.connect` in, `system.session_state`/`system.error`
out, then the connection stays open and Master routes messages between
clients via `internal/session`'s connection registry — `safety.flag`
broadcasts to everyone in the campaign (design doc §9.2), and
`narrative.player_input` is rendered through an LLM (design doc §7's fast
pass only — no slow-pass DM/NPC reaction yet) and broadcast as
`narrative.player_bubble`. Any client can also page through everything recorded for its campaign
with `log.history_request`/`log.history_response` (design doc §10,
§11) — the default (no bounds set) returns the most recent page, with
`before_sequence`/`after_sequence` to page older/newer from there —
since every message exchanged is durably logged to SQLite as it happens.
There's now a real,
usable client to try all of this from, served by Master itself by
default — see [`web/`](web/) and the Running section below — verified in
an actual browser, not just against hand-written test clients. Joining a
campaign can optionally require a password (`-room-passwords`, design doc
§6.6's room-code auth provider) — that's also the seam a future
Discord-OAuth-backed provider is meant to plug into, per that same
section, without reshaping anything (see `internal/auth`). Master can now
dial a real System Engine gRPC sidecar (`-system-engine-addr`, e.g. a
locally running OpenCombatEngine.GrpcSidecar — see `internal/systemengine`)
and calls it for real: `character.upload` sends the uploaded JSON to the
engine's `FromJson`, persists a successfully-parsed character (via the new
`internal/store` `CharacterStore`), and answers with
`character.validation_result` carrying the engine's mechanical warnings
(design doc §9.4). That's the mechanical half of §9.4 only — the
human-veto review panel (`pending_review` → `approved`/`rejected`) isn't
implemented, since it needs a privileged-operator/account concept this
codebase doesn't have yet (only room-password join auth exists); building
it without real authorization would violate CLAUDE.md's "gates over
prompting" rule rather than satisfy it.

Authoritative dice now work too: `roll.check_request` (a player asking to
roll a check for a character they own — enforced via
`store.Character.OwnerID`, the same ownership concept character import
established) calls the system engine's `ResolveCheck` and broadcasts
`roll.request` (so every client's dice tray can pre-stage an animation,
with `roll_spec` derived from the real resolved dice, never assumed —
Master doesn't hardcode "d20" anywhere) followed by `roll.result` (the
authoritative outcome) to the whole campaign, not just the requester —
see `resolveCheck`. This only exists because
`OpenCombatEngine.Core.ICheckManager` was changed upstream to return the
actual `DiceRollResult` (individual dice, not just the total) instead of
a bare int — see that repo's own history for why.

Clients can also read a character back now: `character.schema_request`
forwards the system engine's `GetCharacterSchema` (design doc §4, §6.1)
so a client can render a schema-driven sheet without any system
hardcoded into the UI, and `character.get` answers with a sender-owned
character's current data plus its `GetCharacterStatus` — see
`sendCharacterSchema`/`sendCharacterState`. And a character can now
actually change: `character.apply_effect` calls the system engine's
`ApplyEffect` for a sender-owned character, persists the resulting
`CharacterData`, and answers privately with the fresh `character.state`
— see `applyCharacterEffect`. `effect` is forwarded to the engine
opaquely (an engine-defined shape, same reasoning as `roll.check_request`'s
`check_type`), and the response is deliberately *not* broadcast to the
campaign: who else should see an effect land is design doc §9.7
Knowledge Scoping territory, not decided yet, so this stays as private
as `character.get` rather than guessing at a visibility policy.

The narrative-transform pipeline's slow pass (design doc §7, §8) now
runs too: after the fast pass's literal echo of the player's action,
`runSlowPass` launches a bounded, multi-turn conversation with the LLM,
this time offering it three real DM tools — `resolve_check`,
`apply_effect`, `get_character_status` (see `dm_tools.go`) — backed by
the exact same System Engine RPCs already wired to player-facing
dispatch, not duplicated. Every tool call the model makes is executed
for real, broadcast to the whole table as `tool.result` regardless of
success (§8's call-logging requirement), and fed back into the
conversation; once the model stops calling tools, its response is
broadcast as `narrative.dm_prose`. Design doc §8 says governance gates
(§9) are enforced at this layer — no PvP-policy or maturity-tier engine
exists yet, so `campaignCharacter` (the DM tool lookup helper) enforces
only campaign-scoping, deliberately without the per-owner check
`ownedCharacter` uses for player-triggered actions, since the DM
legitimately acts on any character at the table; this is a documented
gap, not a silent omission. Rules/SRD lookup, procedural generation, and
campaign-notes retrieval are §8's other named tool categories — none of
those exist in this codebase, so they aren't stubbed out speculatively.

The turn-order state machine (design doc §3.1, §9.3) now exists too:
three more DM tools — `start_combat`, `advance_turn`, `end_combat` (see
`turn_order.go`) — give the model a way to trigger it, but the mechanical
bookkeeping is Master's own, independent of the model's judgment.
`start_combat` rolls a real Dexterity check per character through the
System Engine and sorts descending — never trusts the model to invent or
eyeball an order — and silently excludes anyone `get_character_status`
already reports dead rather than failing the whole call. `advance_turn`
walks to the next non-dead character and ends combat automatically once
nobody's left. Every state change broadcasts `turn.state`. In-memory
only, scoped like `session.Hub`'s connection registry — a Master restart
mid-combat loses it.

Unconscious/dying characters are deliberately *not* skipped the way an
earlier version of this codebase treated them — real SRD play still
gives them a turn, they just roll a death saving throw instead of
acting, and skipping them outright would leave them stuck at 0 HP
forever. That death save is now real: a new System Engine RPC,
`StartTurn` (added to `protocol/system_engine.proto` and implemented in
OpenCombatEngine's own `StandardCreature.StartTurn()` — see that repo's
own `feat(creatures): expose StartTurn's automatic death save over gRPC`
commit), runs automatically whenever `startCombat`/`advanceTurn` land a
turn on a character (`startTurnFor` in `turn_order.go`) — SRD's own
mechanic that a dying character re-rolls a death save *every* turn,
never left to the DM to remember or invent. The engine already had this
logic; it just had no RPC exposing it and no way to report what
happened, so it was completely unreachable before this. A rolled death
save persists the character's updated state and broadcasts as a real
`roll.request`/`roll.result` — verified live: bringing a character to 0
HP and advancing the turn produced a real, unprompted death-save roll on
the dice tray, with no explicit player or DM action requested.

Once combat is active, it's now actually enforced on players too, not
just bookkept: `enforceTurnOrder` rejects a player's own
`roll.check_request`/`character.apply_effect` for their character unless
it's currently that character's turn — a player can't roll or act out of
turn once initiative has been rolled, though they act freely outside
combat as before. The DM's own tool calls are deliberately not gated the
same way — see `enforceTurnOrder`'s doc comment for why forcing the
identical mechanical restriction on DM-narrated reactions would be wrong
rather than protective.

**A real limitation
found via live testing, not a hypothetical:** every `character_id` passed
to `start_combat` must be a real `store.Character`. That gap is now
closed: two more DM tools, `get_character_schema` and `create_npc` (see
`dmCreateNPC` in `dm_tools.go`), let the model give a narrated monster/
NPC a real character record — `create_npc` runs the exact same
`FromJson` + persist path `character.upload` uses for a player's own
upload, except the record's `OwnerID` is `masterSenderID` rather than a
player's `sender_id`, so `ownedCharacter`'s per-player gate never
matches it (a player can't directly control a monster) while
`campaignCharacter`'s campaign-scoping gate (which every DM tool goes
through) does. Master's own code stays engine-agnostic throughout — it
never assembles or assumes the character JSON's shape itself (CLAUDE.md:
"don't take a shortcut that assumes... OpenCombatEngine specifically
inside Master code that isn't the system-engine adapter"), so the model
has to actually ask for the schema via `get_character_schema` first and
author `character_json` to match it, the same way a human integrator
would — and now must: `runSlowPass` tracks whether
`get_character_schema` has actually succeeded earlier in the *same* turn
(`schemaFetched`) and rejects `create_npc` outright, before it ever
reaches the engine, if not. This replaced the system prompt's original
"call it if you don't already know the shape" wording, found live to be
a real problem: against a real Ollama server (qwen2.5:32b), the model
routinely skipped the schema call and authored a completely invented,
generic-D&D-flavored document (`race`, `class`, `alignment`,
`weapon_proficiencies`, nested `abilities`...) that doesn't remotely
match OpenCombatEngine's real schema — `create_npc` correctly rejected
it every time. **A real limitation found via live testing, only
partially closed by the gate above:** even after the schema fetch is
forced, the same model's `character_json` attempts still frequently
don't validate — the schema-fetch gate stops it from *skipping* the
schema, it doesn't make the model *follow* one once it's seen it. This
is a model-capability ceiling for complex multi-field JSON authoring,
not something further prompting or gating can fully close; a stronger
model, or a stricter validate-and-retry loop inside `create_npc` itself
(not currently built), would be the next real lever. Either way,
`create_npc` correctly rejects an invalid document rather than silently
accepting one, and (see below) the DM's narration correctly stops
claiming a monster/turn order exists when it doesn't, rather than just
being told not to and sometimes doing it anyway.

**"Stronger model" isn't hypothetical — directly confirmed live:**
re-running the identical 4-goblin-ambush scenario against `qwen3.8:27b`
(this project's actual default `-llm-model`, a newer/larger Qwen
generation than the qwen2.5:32b used for every finding above) instead
produced a completely correct result on the first real attempt: all
four goblins authored and validated on the first try, real initiative
rolled for all five combatants, `start_combat` succeeded, and a full
multi-round exchange — `resolve_check`/`apply_effect` correctly
dropping the player to 0 HP and into a dying state — resolved with
accurate, well-formatted closing narration. This did surface one more
real bug, also now fixed: a fully successful, mechanically-correct
sequence that length (schema fetch, several `create_npc` calls,
`start_combat`, `resolve_check`, `advance_turn`, ...) can exceed
`slowPassMaxToolIterations`, which was tuned against the weaker model's
typically-abbreviated behavior — the *entire* turn then silently
dropped with no narration at all, despite every mechanical step having
already succeeded and already being visible via `tool.result`/
`turn.state`. Raised from 5 to 10 (see that constant's doc comment) to
give a genuinely thorough model room to finish. Practical upshot: which
model is configured meaningfully changes how much of this hardening
actually matters day to day — a self-hoster running a stronger model may
rarely hit the failure paths above at all.

Also found live and now fixed: a chain reaction where `create_npc`
failing meant `start_combat` had no real NPC ID to use, `start_combat`
then failed too (it validates every `character_id` is a real,
already-known character, same as `create_npc`'s own gate exists to
guarantee), and the model's final narration nonetheless announced
"initiative is rolled" and who goes first — directly contradicting the
tool.result already broadcast to the table and the system prompt's own
instruction not to do this. The system prompt's instruction alone
wasn't enough (CLAUDE.md's "gates over prompting," proven necessary
here too): `runSlowPass` now tracks whether `start_combat`/`advance_turn`
failed this turn and, if the model's narration still reads as claiming
turn order was established (`looksLikeUnearnedTurnOrderClaim`), drops
that turn's narration entirely rather than broadcasting the
contradiction — the same "no usable narration this turn" treatment
`looksLikeMalformedToolCall` (below) already gets for a different
failure shape. Verified live, twice, against a real model: the
narration correctly avoided any turn-order claim both times after the
fix, where it had wrongly claimed one before. Also found live: an LLM
occasionally emits a failed tool-call attempt as plain narration text
instead of populating its structured tool-call field —
`looksLikeMalformedToolCall` in `dm_slow_pass.go` catches the common
shapes and drops that turn's narration rather than broadcasting the
artifact to the table.

The DM's own judgment about whether a stated action is actually
*possible* — a spell not currently prepared, movement past a
character's speed — is now grounded in real data instead of a guess:
`runSlowPass` fetches the acting character's own current
`character_data` (best-effort — a character not yet found just means the
turn proceeds without this section, same as before this existed) and
includes it alongside the character ID every turn, and
`dmSlowPassSystemPrompt` explicitly tells the model to check it (a spell
only works if it's in `spellcasting.preparedSpellNames`, movement only
up to `combatStats.speed`) rather than allow whatever's narratively
convenient. This closed a real cross-repo gap found live: OpenCombatEngine's
own schema already modeled the SRD known-vs-prepared-spell distinction
correctly, but `spellcasting` came back `null` after *every* gRPC round
trip regardless — a repository-wiring bug in OpenCombatEngine itself
(`StandardCreature`'s state-restoring constructor needs a real
`ISpellRepository` to resolve spell names, and the sidecar never
supplied one), now fixed there by populating one from the live [Open5e
API](https://open5e.com) at sidecar startup — see that repo's own
`Program.cs`/`ActorMapping.cs`/`SystemEngineGrpcService.cs` and its
README. That first pass was narrative-only, though: nothing stopped the
DM model from calling `apply_effect` directly for a spell's damage
regardless of what `spellcasting` actually said — the model's own
(well-grounded) judgment was the only thing standing between a player
and an unprepared cast, exactly the shape CLAUDE.md's "gates over
prompting" rule exists to close.

A real code-level gate now exists instead: a new `cast_spell` DM tool
and matching System Engine `CastSpell` RPC (`protocol/system_engine.proto`)
are a thin wrapper around OpenCombatEngine's own `CastSpellAction` —
already-tested Core/Implementation logic that checks `PreparedSpells`
(falling back to `KnownSpells` for a non-prepared caster like a
Sorcerer — verified directly against `StandardSpellCaster`'s getter, not
assumed) and slot availability *before* anything happens, and was simply
never reachable over gRPC until now (the third time this exact
"real mechanic, no gRPC exposure" pattern turned up in OpenCombatEngine
this session). `apply_effect`'s own description now steers the model
away from using it for a spell's effect. Live-verified end-to-end
against a real model (`qwen3.8:27b`) and a real sidecar: a wizard
character with Fireball known-but-not-prepared and Magic Missile
prepared got a **rejected `cast_spell` tool result** (`reason_code:
cast_spell_failed`) for the former — the engine's own mechanical
rejection, not the model's narrative judgment — and a **successful
`cast_spell` tool result** for the latter, with the model correctly
narrating the failure/success from the tool result rather than guessing.

**A real, separate bug found during this live verification, since
fixed in OpenCombatEngine:** every Open5e-sourced spell's
`CastSpellResponse.TargetDamaged` came back `false` regardless of the
spell (checked directly via a debug log added and removed for this
verification, across repeated live casts) — `CastSpellAction`'s own
result message was a bare "Cast successfully." with no damage applied.
Root cause, confirmed by reading the code:
`OpenCombatEngine.Implementation/Open5e/Open5eAdapter.cs`'s
`ToStandard(Open5eSpell)` never populated `SpellDto.Damage`/
`DamageInflict`/`SavingThrow` — and Open5e's own REST API doesn't
expose those as separate structured fields for spells at all, only as
prose inside `desc` (e.g. "3d4 + your spellcasting ability modifier
force damage"). Fixed there via a new `Open5eSpellTextParser` that
recovers damage dice/type and saving-throw ability from the SRD's own
consistent phrasing — see that repo's own README/RELEASE_NOTES. Live
re-verified after the fix: a prepared Magic Missile cast now genuinely
damages an unprotected target, and `cast_spell`'s own post-hoc PvP gate
(below) now fires directly through `cast_spell` itself
(`reason_code: pvp_blocked`) instead of the model falling back to a
separate `apply_effect` call once it saw no damage land. The three
adjacent gaps found alongside it — `SpellAttack`/`RequiresAttackRoll`
never populated (and `CastSpellAction` not branching on it at all, so
an attack-roll spell always hit for full damage), `SpellMapper.
MapHealingDice` a permanent stub always returning `null`, and a
multi-instance spell like Magic Missile's three darts only ever
resolving as one instance — are since fixed there too (new `ISpell.
InstanceCount`/`InstanceCountPerUpcastLevel`, a real attack roll vs
the target's AC, prose-based healing-dice extraction). Live
re-verified again after that follow-up fix: a prepared Magic Missile
cast dealt 8 damage (three real 1d4+1 darts, not one), and a prepared
Cure Wounds cast raised the caster from 10/18 to 16/18 HP.

Two more governance gates now exist too (design doc §9.1, §9.5 — see
package `policy` and `campaignPolicy`/`withMaturityConstraint` in
`server.go`): PvP policy is a real mechanical gate — `dmApplyEffect`
blocks a hostile (damage) `apply_effect` against a *different* player's
own character outright unless the campaign's configured policy permits
it (`pve_only`/`pvp_allowed`/`pvp_with_consent`, checked against a
pre-declared consent list for the consent case), never left to the DM
model to self-police; healing another player, or any effect against an
NPC/monster or the acting player's own character, is unaffected.
`dmCastSpell` applies the same policy, but at a different point: Master
can't know in advance whether a *named spell* deals damage (unlike
`apply_effect`'s explicit `effect_type` argument), so there's nothing to
check before calling the engine. Instead the engine reports whether the
target actually took damage (`CastSpellResponse.target_damaged`, a
before/after HP comparison in the sidecar, not text-parsing), and
Master decides post hoc whether to persist that outcome — the caster's
own mutation (slot consumed, concentration set) always persists, since
the cast genuinely happened, but the target's mutation is discarded when
policy would have blocked it. Same net guarantee as `apply_effect`'s
up-front check, just enforced at the commit point instead of the call
point, since that's the earliest point Master actually has the
information. The
maturity tier is (by design doc's own description) prompting-only, not a
hard filter — an operator-authored constraint string appended to both
narrative passes' system prompts when configured, verified live to
actually reach and influence a real model's output on both passes. Both
resolve from a flat JSON file (`-campaign-policies`, design doc §6.6's
precedent for a per-campaign operator setting), not yet §6.4's full
campaign-pack directory tree — see `policy.JSONFileProvider`'s doc
comment for why. A campaign not listed in that file (or the flag left
unset entirely) gets `policy.Default()` — `pve_only` and no maturity
constraint, deliberately the *strictest* PvP setting rather than an open
one, unlike `-room-passwords`' own unconfigured-is-open default.
**A known, honestly-scoped gap:** exercising the PvP gate against a real
DM conversation isn't currently reachable, for the same reason
`create_npc` was needed for monsters — `runSlowPass` only tells the
model about the *acting* player's own character_id, with no campaign
roster, so the DM model has no real way to reference a *different*
player's character in a live conversation today. The gate itself is
real and thoroughly tested (an 8-case table-driven integration test
covers the full policy matrix against the actual `dmApplyEffect` code
path), just not exercisable end-to-end against a live LLM without also
building roster context — a separate, larger piece of work. Turn-order
enforcement hits the same wall for the same reason: a live two-player
"combat starts including both, the wrong one's out-of-turn roll gets
rejected" scenario isn't reachable through natural conversation today
either. Verified live instead was that ordinary out-of-combat play stays
unaffected (a solo `roll.check_request` still succeeds normally when no
`turn.state` is active); the enforcement logic itself is covered by four
integration tests exercising the real `enforceTurnOrder` code path
end-to-end (rejection and success, for both gated message types).

Image generation (design doc §6.3) is now a real pluggable provider
too: a new `generate_scene_image` DM tool calls `imagegen.Provider`
(`internal/imagegen`) — a self-hosted ComfyUI instance is the reference
implementation — and broadcasts the result as `narrative.scene_image`.
Master never constructs the ComfyUI workflow graph itself: since a
checkpoint/sampler/node graph is entirely operator-specific, the
operator exports their own working workflow from ComfyUI's UI ("Save
(API Format)") with the literal token `%%LAYFORGE_PROMPT%%` in place of
the positive-prompt node's text value, and `ComfyUIProvider` just
substitutes the real prompt into it before submitting — the same
"Master stays engine-agnostic" principle as the System Engine boundary,
applied to image generation. Deliberately doesn't implement x402 payment
negotiation — design doc §6.3 describes "existing x402/ComfyUI setup" as
the operator's own deployment shape; a self-hoster whose endpoint sits
behind an x402 paywall needs a transparent proxy handling payment in
front of it, since Master performing real financial transactions
autonomously is out of scope. **Verified live** against a real, running
self-hosted ComfyUI instance — the wire-format assumptions
(`/prompt`, `/history/{id}`, `/view`) built and unit-tested against a
fake HTTP server turned out correct on the first real attempt, no code
changes needed. Live testing also caught one real narration-quality bug:
the model, having seen the raw generated `image_url` in the tool-result
content, echoed it back as a markdown image link in its own DM prose.
Fixed with defense in depth (CLAUDE.md's "gates over prompting," applied
to a formatting concern rather than a mechanical one): the system prompt
now explicitly tells the model never to reference the image in narration
text, and the tool result no longer exposes the raw URL to the model's
context at all — it gets a bare success confirmation, since it has no
actual use for the URL (Master already broadcasts `narrative.scene_image`
to the table separately).

A local-only admin/operator settings panel (design doc §3.3) now exists
too — a second HTTP listener, `-admin-addr` (default `127.0.0.1:8090`),
serving a small tabbed web UI (Campaign / Security / System —
[`admin-web/`](admin-web/)) over a JSON API (`internal/admin`) backed by
two new SQLite tables (`internal/store/admin_settings.go`). Campaign and
Security tab changes (PvP policy, maturity-tier prompts, per-campaign
room passwords) apply immediately: `admin.PolicyProvider`/
`admin.AuthProvider` wrap whatever `-campaign-policies`/`-room-passwords`
already resolved to as a fallback, so nothing configured that way stops
working, and a live end-to-end test confirmed a room password saved
through the Security tab is genuinely enforced by the real `/ws`
listener on the very next join attempt, no restart. System tab changes
(listen address, LLM/System-Engine/ComfyUI endpoints) persist to the same
database but only take effect on a restart, since each is wired into a
long-lived client or listener exactly once at startup — the panel's own
"Save & Restart" button triggers one itself: a graceful shutdown followed
by Master re-executing itself with the same argv (not `syscall.Exec`,
which would skip the cleanup Master's shutdown path already does). Live
end-to-end verified: a System-tab value saved and restarted with came
back correctly in the new process (overriding the flag default it booted
with the first time), and the admin page's own poll-and-reload picked the
new process back up automatically. See `main.go`'s package doc comment
for a systemd `KillMode` caveat this self-restart interacts with.

The `map.*` message category (design doc §6.2) is now real too: a new
`generate_combat_map` DM tool (called only when a fight's physical space
is actually worth tracking, the same restraint `generate_scene_image`
already uses — not automatic on every `start_combat`) generates a grid
map natively in Go (`internal/combatmap` — a rooms-and-corridors
generator, no third-party dependency, matching Master's own
single-static-binary rule) and auto-places every combatant's token,
clustered by team. Distance/line-of-sight tracking is real: each
connected player gets their *own* fog of war (recursive shadowcasting
against the generated blocking grid, computed server-side) — genuinely
different `map.token_state` content per recipient for the same event,
sent via a new `session.Hub.SendToSender` (there was previously no way
to target one specific player's connection at all; only room-wide
`Broadcast` or replying to whichever connection triggered the request
existed). `map.token_move_request` validates token ownership, movement
speed (`combatStats.speed`), and the blocking grid before accepting a
move, then re-sends every affected player's own updated view — moving
can reveal or hide things for someone other than the mover, e.g.
stepping past a corner.

Each recipient's `map.token_state` also carries a composited PNG
(`image_url`, Go stdlib `image`/`image/png`, no external service) of
their own fog-of-war-filtered view — the reference web client shows this
as a static sidebar thumbnail, click to enlarge in a lightbox, live-
verified in a real browser end-to-end: `generate_combat_map` ran as part
of a real DM tool-call chain (`create_npc` → `start_combat` →
`generate_combat_map`) against `qwen3.8:27b`, the sidebar thumbnail
appeared showing a real generated room, and the lightbox opened/closed
correctly (click, the close button, and Escape all worked). One honest,
observed rough edge from that same run: the auto-placement heuristic
(cluster the first-seen team near one scan-order extreme, everyone else
near the other) doesn't guarantee two teams land in the same room or
even within sight of each other — in the live run, the goblin ended up
outside Kestrel's own fog of war despite the DM's own narration
describing them as being in the same room together. Mechanically
correct (fog of war is doing exactly what it's supposed to with the
positions it was given), just a rough narrative/placement mismatch worth
a better placement heuristic later, not silently glossed over here.

**Since closed**: grid/position data now does reach the System Engine for
`cast_spell` specifically — `dmCastSpell` (`dm_tools.go`) calls a new
`buildGridContext` (`combat_map.go`) that, whenever a combat map exists
for the campaign and both the caster and target have tokens on it,
populates `CastSpellRequest.grid_context` with real positions and the
generated map's own line-of-sight-blocking cells. OpenCombatEngine's own
`CastSpellAction` already had a complete, tested range/line-of-sight
check (`ParseRangeInFeet` vs. `IGridManager.GetDistance`,
`HasLineOfSight`) sitting dormant since it never received a populated
grid to check against — this makes that check receive real data for the
first time, so a spell cast at a target genuinely out of range, or
behind a real generated wall, is now hard-rejected by the engine rather
than resolving regardless of distance. Casting with no combat map
generated (still the common case) is completely unaffected — no
`grid_context` is sent, `context.Grid` stays `null`, and `CastSpellAction`
skips the check exactly as it always has.

Still deliberately out of scope, flagged rather than silently built
around: **cover** remains genuinely unbuilt in OpenCombatEngine, not just
unwired — `IGridManager` has no cover-computation method at all
(`CoverType` is mechanically real only where `StandardCreature.
ResolveAttack` already used it directly, e.g. a manually-supplied
`AttackResult`), so there was no capability to wire even if this pass
wanted to. `apply_effect` still has no range/LOS concept at all (it's
for narratively-clear non-spell effects, with no `Range` field to check
against). No token art (plain colored circles), no cave-style generation
(one room/corridor generator only), no interactive drag-and-drop
placement (auto-placed at generation, moved only by declaring a
destination cell), and combat-map state is in-memory only — lost on a
Master restart, same documented limitation `turnOrder` already has.

**Since closed**: weapon attacks are now gated too, not just spells —
`melee_attack` and `ranged_attack` are two new DM tools (`dm_tools.go`'s
`dmAttack`, dispatched with a different `AttackKind`) wrapping
OpenCombatEngine's own already-tested `AttackAction` over a new `Attack`
RPC, the same "gates over prompting" shape `cast_spell` established:
before this, a fighter or rogue's only option was `apply_effect`, which
has no weapon-legality or range/LOS concept at all — nothing but the DM
model's own narrative judgment stood between a player and an attack that
shouldn't be possible with their equipped weapon. The engine now checks
the attacker's actual equipped main-hand weapon and rejects the call
outright when it can't do what's being asked — `ranged_attack` with a
weapon lacking both `Thrown` and `Ammunition` (a longsword), or
`melee_attack` with an `Ammunition`-only weapon (a bow) — a real
mechanical gate on the weapon's own SRD properties, not a guess.
`buildGridContext` is reused unchanged from the `cast_spell` work — it
was already fully generic, not spell-specific, so `dmAttack` gets real
range/line-of-sight gating for free.

Finding and closing this also surfaced a second, unrelated bug in the
same "real data silently dropped" family that `CastSpellResponse.
target_damaged`'s earlier fixes were about (see OpenCombatEngine's own
RELEASE_NOTES.md): `ActorMapping.ToCreature` never had a real
`IItemLibrary` to resolve inventory item names back into live weapon/
armor objects, so **every equipped weapon silently came back unequipped
after a gRPC round trip**, regardless of what a character actually had
equipped — the same shape as the earlier bug where spellcasting always
came back `null` before `ISpellRepository` was wired in. Fixed the same
way: OpenCombatEngine's sidecar now populates a real `IItemLibrary`
(`StandardItemLibrary`, from Open5e's weapon/armor/magic-item endpoints)
at startup alongside the spell repository, and threads it through
`ActorMapping.ToCreature` into every RPC that reconstructs a creature.
Also closed a genuine "real data thrown away" parsing gap while wiring
this: Open5e's own weapon `properties` strings already embed a weapon's
real range (`"thrown (range 20/60)"`), but the existing parser only kept
the leading word (`"thrown"`) and silently discarded the number —
`IWeapon` now has a real `Range` (feet) property, parsed from that same
text rather than a hardcoded lookup table, with an SRD-accurate fallback
(10 ft for `Reach`, 5 ft otherwise) when a weapon's properties don't
carry one.

**Verified live** against the real Open5e API (1709 real SRD
weapons/armor/magic items loaded into the sidecar's item library at
startup — small enough that, unlike the ~1400-spell catalog, no local
cache was built for it yet), the gRPC sidecar, and a real downstream
consumer (Master + a real LLM, `qwen3.8:27b`): a character with a real
equipped Longsword called `melee_attack` against a goblin NPC, the
engine rolled a real attack and dealt real damage (the goblin dropped
from 7 HP to barely conscious), and both the attacker's and target's
mutated state persisted — the entire chain (Open5e → item library →
`ActorMapping` → `AttackAction` → `dmAttack` → `store.Character`) proven
working end to end, not just unit-tested. The weapon-kind *rejection*
path was **not** proven live the same way: across two real attempts
(a Shortbow-equipped character narratively describing a melee swing),
the DM model resolved the outcome purely in prose without ever calling
`melee_attack` at all — a real, honestly-reported finding, not a
rejection the engine actually issued. That branch is covered instead by
deterministic tests on both sides (`Attack_MeleeAttackWithBowEquipped_
ReturnsFailure`/`Attack_RangedAttackWithLongswordEquipped_ReturnsFailure`
in OpenCombatEngine, `RangedAttack_EngineRejects_
ReturnsFailureToolResult` here) — same "can't control what a live LLM
session does" caveat this repo's own `cast_spell` range-rejection test
already documented for the identical reason.

Still deliberately out of scope, flagged rather than silently built
around: the SRD "disadvantage on a ranged attack while a hostile
creature is within 5 ft" rule, and long-range (the second number in
Open5e's own `"(range X/Y)"` text) disadvantage — both would require
touching `AttackAction` itself, which this pass deliberately left
untouched since it was already correct and tested. No explicit
weapon-choice argument either — a `melee_attack`/`ranged_attack` call
always resolves against whatever's in the main hand, same "already-
equipped, don't re-litigate inventory" reasoning `cast_spell` uses for
known/prepared spells.

**Since closed**: a new `get_available_actions` DM tool computes the
*full concrete menu* a character actually has right now — every
equipped-weapon attack option (melee, ranged, and a genuinely new
off-hand/secondary-weapon bonus-action attack, per SRD Two-Weapon
Fighting), Grapple and Shove options, and every currently-castable
prepared/known spell, each against every other character currently in
this campaign's active combat (`combatParticipantIDs`, reusing
Master's own turn-order state rather than inventing a separate
campaign-roster concept). This is your own direct correction to an
earlier, narrower proposal of mine this session — you wanted "the full
list of things available so that the player has all those choices to
pick from," not an abstract "is melee legal: yes/no" summary. Building
it surfaced that OpenCombatEngine's own `ICreature.Actions` — which
looked like a ready-made menu — actually under-represents a player
character's real options (it only ever yields a hardcoded "Unarmed
Strike," ignoring `Equipment.MainHand`/`OffHand` entirely), so the new
`GetAvailableActions` RPC derives weapon options directly from
equipment instead, the same way `Attack`'s own handler already did.

Two real mechanics that didn't exist anywhere in OpenCombatEngine
before this — **Grapple** and **Shove** — were built for real as part
of this (only the resulting `Grappled`/`Prone` conditions existed;
nothing ever applied them), composed entirely from primitives already
tested elsewhere (opposed Athletics/Acrobatics checks, condition
application, forced movement). A genuine, previously-unenforced SRD
rule also got closed everywhere it applies: a source creature that is
itself Paralyzed/Stunned/Petrified/Incapacitated can no longer attack,
cast, grapple, or shove at all — before this, only a *target's*
incapacitating conditions were ever checked (for advantage).

That same incapacitation fact now drives turn order directly:
`advanceTurn`/`start_combat`'s shared search
(`advanceToNextActionableCharacter`, `turn_order.go`) auto-skips an
alive-but-incapacitated character's turn with a real, Master-composed
narration (not routed through the DM's own LLM pass — this session's
own live verification of `melee_attack`/`ranged_attack` found the
model narrating around a real gate rather than calling the tool that
would have surfaced it, so a turn-skip announcement can't be left to
its judgment either) — this is exactly the "a paralyzed character
should get skipped in the initiative order" gap you raised directly.
A full lap finding nobody able to act does not end combat the way
"everyone's dead" correctly does (a real, temporary battlefield state,
not the end of the fight) — it lands on the last alive-but-
incapacitated character found, so cycling `advance_turn` keeps ticking
condition durations down each round until someone can act again.

**Verified live** against a real sidecar and a real LLM (`qwen3.8:27b`)
— with one honest asterisk: Open5e was genuinely unreachable during
this verification run (confirmed via direct `curl` timeouts), so the
weapon-dependent options (melee/ranged/off-hand/Grapple/Shove) couldn't
be exercised live this time — that path is covered by the deterministic
test suite instead (both repos). What did verify live: a character
with a real prepared spell called `get_available_actions`, and the
DM's own narration — generated only from the real tool result — 
correctly reported the prepared spell and full action economy; and a
real `Paralyzed` condition applied via `apply_effect` produced the
exact deterministic skip narration ("Kestrel is Paralyzed and cannot
act this turn.") when `start_combat` searched for the first actionable
character.

**Since closed**: `get_available_actions` used to only advertise that
Grapple, Shove, and an off-hand attack were legal, with no way to
actually take one — flagged honestly at the time rather than shipped
silently. Three new DM tools close that: `grapple` and `shove` call new
`Grapple`/`Shove` RPCs exposing `GrappleAction`/`ShoveAction` over gRPC
for the first time, and `offhand_attack` reuses the existing
`melee_attack`/`ranged_attack` machinery (`dmAttack`) with a third
`AttackKind` — `ATTACK_KIND_OFFHAND`, which resolves against
`Equipment.OffHand` instead of `MainHand`, always as a bonus action,
never adding the ability modifier to damage (SRD Two-Weapon Fighting's
core rule). `grapple`/`shove` apply the identical post-hoc PvP gate
`melee_attack`/`cast_spell` already established, keyed on whether the
attempt's real mechanical effect (`Grappled`/a shove landing) — not
damage — actually applied to a different player's character.

**Verified live**, same real sidecar/Master/`qwen3.8:27b` stack:
Grapple and Shove need no equipped weapon at all, so a later pass
verified both live even with Open5e still down — a real `grapple` call
succeeded with the DM narrating actual opposed-check numbers straight
from the tool result ("its Athletics roll of 4 against Kestrel's 16"),
and a real `shove` call knocked the target prone with an equally
grounded narration. The DM model called `get_available_actions`
unprompted before attempting the shove — the "check what's legal
before acting" workflow this session's own tool descriptions were
written to encourage, actually observed happening on its own.
`offhand_attack` remains live-unverified (needs real equipped-weapon
data, blocked by the same Open5e outage) — covered by
`Attack_OffhandKind_*` in OpenCombatEngine's own test suite instead.

**New**: five DM tools — `equip_item`, `unequip_item`, `receive_item`,
`discard_item`, `give_item` — close a gap that was there from the
start: until now, `character.upload`'s JSON set a character's equipment
and inventory exactly once, and nothing afterward — no DM tool, no
player action — could equip a different weapon, don armor found in
play, pick something up, drop something, or hand an item to another
character. This was scoped narrower than your original ask on purpose:
you also asked about off-site possessions (a mount and its saddlebags
left outside a dungeon), land holdings for advanced campaigns, combat
loot, and death-triggered inheritance distribution when a party doesn't
resurrect a fallen character. Research confirmed all four are real,
separate, currently-unbuilt pieces — `ILootGenerator` exists engine-side
and is fully unconnected to any distribution logic; nothing in either
repo models a location or "left behind" concept outside an active
combat grid; nothing walks a dead character's belongings when
`IHitPoints.Died` fires. Each is a natural next pass once these five
tools exist for it to call into, not silently dropped — see
[`docs/design.md`](../docs/design.md) for where a future pass should
pick this up. `equip_item`/`unequip_item`/`receive_item`/`discard_item`
carry no extra gate beyond ordinary campaign-scoping, the same
GM-level latitude every other DM tool already has over any character at
the table; `give_item` alone gets a PvP gate, but a differently-shaped
one than `grapple`/`melee_attack`/`cast_spell`'s post-hoc pattern — it's
checked *before* calling the engine at all, since an item transfer has
no "the attempt still happened, only the outcome is blocked" half-state
worth preserving the way a failed grapple attempt does. The gate is
keyed on the *giving* character's owner relative to the player whose
narrative triggered the tool call, not the recipient's — handing
something to a different player's character is never hostile; taking
something away from one without that player's own action driving it is.
`receive_item` resolves the named item against the sidecar's real
Open5e-backed item catalog exactly like every other tool this session
that touches named content — an unrecognized name is a real rejection,
never invented.

**Not verified live this pass**: Open5e was unreachable from this
environment during this pass's verification window (repeated direct
`curl` timeouts against `api.open5e.com`, same outage
`get_available_actions`/`Grapple`/`Shove` hit earlier), and unlike
spells, OpenCombatEngine's item library has no local on-disk cache yet
(`StandardItemLibrary`/`Open5eContentSource` are network-only —
confirmed by reading the sidecar's own `Program.cs`, which already
flags this as a known future gap in its own comments) — so with Open5e
down, the sidecar starts with an *empty* item library and
`receive_item`/`equip_item` against a real catalog item genuinely
cannot be exercised live right now, not just "wasn't tried." Reported
honestly rather than skipped silently, same as this session's two
earlier Open5e-outage passes. All five tools are instead covered by the
deterministic test suites on both sides: 16 new `SystemEngineGrpcServiceTests`
in OpenCombatEngine (success/rejection/attunement-on-transfer/
equipped-item-auto-unequips-on-transfer for each RPC) and 15 new tests
in `master/internal/server/inventory_test.go` (success/persistence,
engine-rejection, invalid-slot rejection, and a 4-case `give_item`
PvP-gate table mirroring `grapple`/`shove`'s own).

**New**: three DM tools — `generate_loot`, `add_currency`,
`transfer_currency` — close the next deferred piece from that same pass:
combat loot. `ILootGenerator`/`StandardLootGenerator` existed
engine-side but were fully unconnected to anything; this wires them up,
plus real currency, for the first time. You corrected the intended
timing directly: as a DM, you generate treasure *before* an encounter,
not after — "it didn't make sense for a wizard enemy to not use the
wand of magic missiles he had in a fight." So `generate_loot` runs at
encounter-prep time against a not-yet-fought roster (typically right
after `create_npc`-ing the monsters), and the DM places pieces of the
result directly onto specific NPCs with `add_currency`/`receive_item`/
`equip_item` — the wand goes on the wizard, not a random guard — so
they can actually carry and use what they're holding when the fight
starts. Post-combat looting is then just moving whatever's genuinely
left in a dead NPC's own inventory: `give_item` (already built) for
items, and the new `transfer_currency` for coin.

You also asked specifically that combining multiple defeated creatures'
challenge ratings into one group-appropriate loot roll (your example:
an evil high priest, three acolytes, and ten guards) be computed by the
*system engine*, not by Master — "if the game engine can calculate it
from the enemies, I'd prefer that over anything outside the engine,
because someone might want a Vampire: Masquerade engine." That's the
same boundary CLAUDE.md already draws (§6.1): `generate_loot` sends
Master's real `Actor` records straight through to the engine's new
`GenerateLoot` RPC, and the CR→XP-budget math happens entirely on that
side, in a new `IEncounterChallengeCalculator`/
`StandardEncounterChallengeCalculator` — Master never computes or holds
a raw CR number itself. Every named character needs a real
`challenge_rating` on its record (authored at `create_npc` time, same
as every other stat) or the call is rejected — never an invented
default. `transfer_currency` gets `give_item`'s exact PvP-gate shape
(checked before calling the engine, keyed on the giving character's
owner, not the recipient's); `generate_loot`/`add_currency` get no
extra gate, same latitude every other DM tool has.

**Verified live** against a real sidecar, Master, and `qwen3.8:27b`,
with two honest asterisks. First: Open5e's item catalog still failed
to populate at sidecar startup even though Open5e itself was reachable
again by this pass — a real, live-observed instance of the exact gap
OpenCombatEngine's own README/RELEASE_NOTES already flag (no local
cache for the larger, uncached weapons/armor/magic-item fetch yet, so
it hit the sidecar's 15-second timeout) — so `generate_loot`'s item
slot and `receive_item`/`equip_item` against a real catalog item
weren't exercised this pass either. What *did* work end to end, with
zero item-library dependency: the DM created a real CR-0.25 goblin NPC
via `create_npc`, called `generate_loot` against it, and got back real
dice-rolled coin (2800 copper, 1800 silver, 80 gold, all within
`StandardLootGenerator`'s own Tier-1 ranges), then called `add_currency`
to place 80 gold directly into the goblin's own inventory before the
fight — narrated correctly from the real tool results throughout,
including the DM's own reasonable call to track the rest as party loot
rather than dump it all on one goblin (no even-split algorithm exists
or was expected to — same "left to DM narrative judgment" precedent
`give_item`'s own distribution already relies on). Second asterisk:
post-combat looting (`give_item`/`transfer_currency`) wasn't exercised
live this pass, for a live-test-harness limitation rather than a
product one — the scratch script had no way to hand the DM the real ID
`create_npc` had generated for the goblin in a later, separate
narrative turn, and the DM correctly asked for it rather than guessing
one to call `transfer_currency` with. That's the "gates over prompting"
never-invent-an-identifier principle actually holding, live, not a
defect — `transfer_currency`'s own mechanics (a real transfer between
two inventories, and a real rejection when source can't afford the
amount) are proven instead by its deterministic test suite on both
sides.

**Since closed**: `give_item`/`transfer_currency`'s PvP gate was keyed
purely on `source.OwnerID`, with no exception for a source that's
already dead — meaning the party dividing up a fallen ally's own gear
(who's carrying the body and coin from here on) got wrongly blocked as
PvP theft under this campaign's default `pve_only` policy, the exact
scenario `generate_loot`/corpse-looting exists to support. You corrected
the framing directly: this was never a "does the party choose to raise
them" workflow the AI DM should proactively ask about — that
conversation stays entirely the players' own — it's simpler than that,
a fallen character's own things mechanically need a living carrier, same
logistics as splitting up an enemy's loot. Both tools now call
`characterIsDead` (`turn_order.go`'s own real, engine-computed status
check — the same one `advance_turn`/`start_combat` already use, not the
DM model's own say-so) and skip the gate entirely when the source is
dead; a merely-unconscious/dying character (0 HP, not yet dead) still
keeps the same PvP protection as any other player character, since real
SRD play still gives them their own turn to roll a death save. Two new
PvP-gate table cases (a dead, differently-owned source succeeding even
under `pve_only`) cover this in both `inventory_test.go` and
`loot_test.go`, alongside the existing living-source-still-blocked cases
proving the gate otherwise holds exactly as before.

Off-site possessions (stashes, mounts/carts/wagons/ships) and land
holdings are all closed further below, once a real campaign-pack loader
exists to give "somewhere" a real meaning. The buy/sell/vendor economy
piece is closed below too.

**New**: session persistence and a real admin campaign list. You raised
this after noting a campaign is "a live, mutable session" — players
need to be able to save progress and reconvene later, and the host
needs visibility into which sessions exist. The gap turned out narrower
than it sounded: character state, the event/audit log, and campaign
settings (PvP/maturity policy) already persisted to SQLite continuously
— every mutation wrote through immediately, nothing was lost there.
The real gap was exactly two things, both explicitly flagged in their
own doc comments as "lost on Master restart": active combat's turn
order/initiative (`Server.turnOrders`) and the combat map/fog-of-war
state (`Server.combatMaps`), both in-memory-only. Both now write
through to a new `combat_state` table on every mutation (start_combat,
advance_turn, generate_combat_map, a token move, mirroring how character
saves already work — real-time, not periodic), and a new
`WarmUpCombatState` startup pass rehydrates every campaign's persisted
snapshot back into memory before Master starts accepting connections —
every existing in-memory-map read site needed no changes at all as a
result, since the maps are already warm by the time any request touches
them.

For the admin-visibility half, you picked "admin visibility only" over
a session start/stop model: Master keeps serving every campaign
simultaneously exactly as it always has (a campaign_id is always
available the instant anything references it — no start/stop concept
exists or was added). `GET /api/campaigns` used to return just a flat
list of IDs for a bare settings dropdown; it now returns real party
size, last-played timestamp, and archived status per campaign, backed
by a new `campaign_meta` table (kept deliberately separate from
`campaign_settings`, whose own save is a full-replace — coupling new
fields into that struct risked silently clearing them on every ordinary
policy save). You also asked for the host to be able to name a campaign
when they create it, with that name persisted and shown back so they
can actually recognize it later, rather than hunting through bare
campaign_id slugs — a new `POST /api/campaigns` action does exactly
that (upserting a display name against a campaign_id without touching
how it's joined, played, or governed), and a new
`PUT /api/campaigns/{id}/archive` action lets the host get old sessions
out of the admin panel's way — archiving is purely a display filter,
verified live: a real player connection successfully joined a real
archived campaign over `/ws`, exactly as designed.

**Since closed**: permanent campaign deletion. Archive (soft,
reversible) covered everything the prior pass asked for; this closes
the "deliberately deferred, materially more dangerous" follow-on it
named. Because this permanently destroys real data — a campaign's
characters and its entire event/audit log — it gets a real gate, not
just a confirmation dialog: a new `DELETE /api/campaigns/{id}` only
succeeds when the campaign is already archived, checked inside the
same transaction that does the deleting (`SQLiteEventStore.
DeleteCampaign`, `store.ErrCampaignNotArchived` on rejection) — a
caller bypassing the admin UI entirely still can't delete a live
campaign out from under a table. One transaction removes every row for
that `campaign_id` across every table that references it —
`characters`, `events`, `campaign_settings`, `campaign_meta`,
`combat_state` — all gone or none are. The admin UI's own "Delete"
button only appears on an already-archived row (never a live one) and
requires typing the campaign's own `campaign_id` into a confirmation
field before it enables, a deliberately higher-friction step than a
bare browser `confirm()` a habituated click can blow through.

**Verified live**, same real Master process/real file-backed SQLite
database as the archive feature above: `DELETE` on a freshly-created,
unarchived campaign was rejected (400, `ErrCampaignNotArchived`,
nothing removed); a real character and event were seeded for a second
campaign, which was then archived and deleted through the real HTTP
API, and both the campaign's admin-panel listing *and* its underlying
character/event rows were confirmed genuinely gone afterward (checked
by opening the same database file directly, not by trusting the API's
own say-so) — a second, untouched campaign's own rows in the same
tables were left alone by every test covering this, proving the
deletes are scoped by `campaign_id`, not a wholesale wipe.

**New**: a vendor buy/sell economy — the last of the three deferred
follow-ons named above. Earlier attempts to source item-price data
externally (a dndbeyond forum post, toolsandtaverns.com, a
thievesguild.cc price list, a "SRD" dandwiki page that turned out to be
3.5e OGL content) were all declined on copyright/non-SRD grounds. The
real fix needed no external source at all: OpenCombatEngine's
`IItem.Value` (its Open5e-backed item library already carries real SRD
price data) was simply never exposed over the gRPC contract — and, a
genuine bug found while adding a real reader for it,
`Open5eItemMapper.ParseCost` truncated anything under 1 gp to **0**
(`(int)(1 * 0.01)` for "1 cp" → `0`) via its own `// Let's assume Gold
for now` comment. Fixed alongside the new RPCs (companion PR in that
repo): `Value` is now denominated in copper pieces, losslessly.

Two new, narrowly-scoped, read-only RPCs close the actual gap:
`GetItemInfo(item_name)` (the item library's real price, minimal-coin
decomposed) and `ListInventory(actor)` (an actor's real held item
names — needed because `character_data` is opaque to Master by design;
parsing "inventory" out of it directly here would be exactly the kind
of D&D-specific shortcut CLAUDE.md's system-engine boundary rule
forbids). Both are pure data lookups, no rules judgment, so they're a
low-risk contract addition.

**Vendor = an ordinary NPC character**, stocked with the tools that
already existed — `receive_item`/`add_currency` — no new "create a
vendor" tool needed. Four new DM tools: `check_item_price` and
`list_vendor_inventory` (read-only, real price/stock, never an abstract
"yes they have goods" summary — the same "full enumeration, not a
summary" principle you established for `get_available_actions`), and
`vendor_sell_item`/`vendor_buy_item`, each a real `TransferItem` leg
followed by a real `TransferCurrency` leg (price from `GetItemInfo`
times a new per-campaign `price_multiplier` — a host-set "world
information" economic knob, admin-panel-editable, applied on top of the
engine's authoritative base price, resolved to 1.0 when unset). Turned
out simpler than planned: since every system-engine RPC is stateless
(no creature state held between calls) and Master only commits via an
explicit `SaveCharacter`, nothing is actually persisted until *both*
legs of a sale succeed — a second-leg failure (the buyer can't afford
it) needs no compensating reverse RPC, since the store still holds each
character's untouched original state. The item-source leg of each tool
(`vendor_sell_item`'s vendor, `vendor_buy_item`'s seller) reuses the
same PvP gate `give_item`/`transfer_currency` already had — extracted
into one shared `pvpGateBlocked` helper rather than a fourth copy of
the same ~15-line block.

Inherited, not introduced by this pass: `TransferCurrency` still makes
no change across denominations, so a buyer holding the right *total*
value in the wrong coins can still be rejected — the live test below
hit this for real.

**Verified live**: a real `OpenCombatEngine.GrpcSidecar` process (its
Open5e item-fetch has no network access in this environment, so it was
pointed at a hand-seeded local item cache holding two real SRD
weapons — Longsword 15 gp, Dagger 2 gp — via `OPEN5E_ITEM_CACHE_PATH`,
the same on-disk cache format the sidecar already uses to avoid
re-fetching from Open5e on every real startup) and a real Master
process talking to it over actual gRPC, narrated by the real
qwen3.8:27b model on the LAN Ollama server, driven over a real
WebSocket connection: a shopkeeper NPC was stocked with a real
Longsword via a real `AddItemToInventory` call; a buyer with 20 gold
and no platinum asked to buy it in natural language — the model
correctly called `check_item_price`, priced it at 1500 copper (1
platinum + 5 gold, the real decomposition of the real 15 gp SRD price),
called `vendor_sell_item`, and got a real `insufficient_funds`
rejection — the buyer's gold-only purse couldn't cover the platinum
piece the price required, exactly the inherited denomination limitation
above, not a bug. The database was confirmed unchanged for both
characters afterward. The buyer was then funded with one real platinum
piece (`AddCurrency`); the same request succeeded end to end —
`vendor_sell_item` returned success, and the underlying SQLite rows
confirmed the real result: the shopkeeper's Longsword gone and 1
platinum + 5 gold richer, the buyer holding the Longsword with exactly
15 gold left.

**New**: a real campaign-pack loader (design doc §6.4) — the
foundation the final deferred item from the loot pass needed: off-site
possessions and land holdings, closed further below once locations mean
something real to be "off-site" from. `campaign-packs/sable-ravine/`
was a real, committed example
pack from an earlier pass, but Master never parsed it; it was
hand-fed into the DM's context for its own playtest. A new
`internal/campaignpack` package now parses a pack directory for real —
`campaign.md`, `locations/*.md`, `npcs/*.md`, `encounters/*.md`, each
markdown + YAML front matter — with a real, table-driven test suite run
against that same committed fixture directory, not a hand-built one.
Deliberately does not load `state.json`: mutable session state (party
location, discovered locations, stashed possessions, land holdings)
moves into Master's own SQLite store instead (`campaign_pack`,
`party_location`, `location_state`, `stashed_items`, `stashed_currency`
tables), matching how every other piece of live campaign state already
persists, rather than a running server rewriting a file tracked in the
pack's own git history — `state.json` stays what its own comment always
said it was: the starting shape, not something read at runtime.

A host binds a pack directory to a campaign via a new admin action
(`PUT /api/campaigns/{id}/pack`, admin-web's Campaign tab) — validated
by actually parsing the directory first, so a bad path is a real
rejection, not a silent no-op that only breaks the next time the DM
tries to use a location tool. Once bound, seven new DM tools exist:
`list_locations` (the real connection graph, discovered/claimed state —
same "full enumeration, not an abstract summary" principle
`get_available_actions`/`list_vendor_inventory` already established),
`travel_to` (a real gate: only legal to a location in the *current*
one's real `connections`, or anywhere at all for the party's very first
move), `stash_item`/`retrieve_item` and `stash_currency`/
`retrieve_currency` (off-site possessions — retrieve only succeeds for
something actually stashed at the party's *current* location, a real
mechanical consequence of "you have to be there," not a lookup
convenience), and `claim_location` (land holdings — a real, persistent
flag+note on a location, proportionate in scope to how tersely this was
named in the first place, no ownership-contest mechanic). A new
`RemoveCurrency` RPC (companion PR, mirroring `AddCurrency`'s shape)
closes a real gap `TransferCurrency` couldn't: debiting a character into
a location-scoped stash that isn't itself a creature. Turned out
simpler than planned once implementing it: since every system-engine
call is stateless and Master only commits via an explicit
`SaveCharacter`, no compensating rollback logic was needed anywhere in
this pass either, the same realization the vendor-economy pass above
already made.

The DM's own context is grounded in the real current location
(`runSlowPass`'s existing best-effort userContent sections gained a
"Current location: ... (connects to: ...)" line) so it doesn't have to
call `list_locations` on every single turn just to know where the party
is. And `pvp_policy` — one of the two fields design doc §9.1/§9.5's
governance gates actually need — now really can resolve from a bound
pack's own `campaign.md` front matter (a new
`admin.CampaignPackPolicyProvider`, layered between the admin panel's
own explicit override and the flat `-campaign-policies` JSON file),
closing the exact interim scope `campaign-packs/README.md` named.
`maturity_tier` is deliberately NOT resolved this way: design doc §6.5
defines it as a *reference* to a separate `maturity_tiers/<id>.md` file
(the real prompt-constraint text lives there, not in `campaign.md`
itself), and that loader doesn't exist in this codebase yet — copying
campaign.md's raw tier name into a prompt-constraint field would be
wrong, not just incomplete, so this stays a real, named, separate
follow-on rather than a broken shortcut.

**Verified live**: a real `OpenCombatEngine.GrpcSidecar` + a real Master
process + `qwen3.8:27b` over a real WebSocket connection, with the real
`campaign-packs/sable-ravine/` directory bound through the real admin
API. `list_locations` returned and the DM correctly narrated all six
real locations and their real connections, correctly noting the party
hadn't set foot anywhere yet. A bootstrap `travel_to keep-stonewatch`
succeeded and persisted; an illegal `travel_to ruined-shrine` (not
directly connected) was rejected with `not_reachable`, and the DM
correctly narrated the real multi-hop path required instead of just
failing silently. `stash_item` moved a real Longsword out of the
character's inventory into a real stash at keep-stonewatch; a
`retrieve_item` attempt after traveling to old-road correctly failed
with `nothing_stashed_here` (proving the location gate, not just the
existence check); a genuine model mistake was caught and reported
honestly rather than hidden — a later retrieval attempt passed the
character's internal engine UUID instead of its store character_id,
plus a lowercase `"longsword"` instead of the exact stored
`"Longsword"`, and was correctly rejected on both counts (this system's
existing exact-match convention, not a new gap); a follow-up call with
the correct identifiers succeeded, moving the item back into the
character's real inventory and clearing the stash. `stash_currency`
moved a real 10 gold out of the character's currency (20 → 10) into a
real per-location stash (confirmed directly against the database, not
just the tool result), and `claim_location` persisted a real
`claimed_by_party = true` with the exact note given. Real engine-backed
`pvp_policy` resolution from `campaign.md` (rather than through the
admin API, which shows what's explicitly configured, not what's in
effect) is covered by `CampaignPackPolicyProvider`'s own deterministic
tests instead, which read the real `pve_only` value out of the real
committed `campaign.md` file.

**New**: `list_npcs` and `list_encounters` — the campaign-pack loader
above parsed `npcs/*.md`/`encounters/*.md` from day one, but nothing
read `Pack.NPCs`/`Pack.Encounters` until now, the same "real reader
finally exists" shape as `IItem.Value` before the vendor-economy pass.
Both are read-only, full-enumeration DM tools (`campaignPackTools()`,
renamed from `locationTools()` now that it covers more than location
mechanics) — id, home location, a `stat_block_ref` hint for `create_npc`
to build a mechanical record from, and real voice/personality text for
NPCs; id, location, involved NPCs, and the full real setup/trigger text
for encounters (the actual DC checks and branching, not a summary) —
so the DM checks what was pre-authored before improvising a generic
NPC or fight from nothing. Neither carries mutable per-campaign state
of its own (no discovered/claimed equivalent) — a direct pass-through
of whatever `LoadPack` parsed, gated the same "no campaign pack bound"
way every other campaign-pack tool already is.

**Verified live**: real sidecar + Master + `qwen3.8:27b`, the real
`sable-ravine` pack bound. `list_npcs` correctly narrated all four real
NPCs (Captain Orlen Vashti, First Digger Nix, Goblin Chief Skreel, the
Hollow Flame) with real voice/personality detail, not invented ones.
`list_encounters` — asked in the same session — called
`list_encounters` *and* `list_npcs` *and* `list_locations` together
unprompted to build one coherent briefing, correctly citing the real
DC 13 Wisdom ambush check, the parley-or-raid branch at the goblin
camp, the two captive scouts, the kobolds' information-for-safety
trade, and the shrine's two-layer guardian/Hollow-Flame structure —
all pulled from the real committed encounter/NPC text, not
paraphrased summaries.

**New**: a real `maturity_tiers/*.md` loader (design doc §6.5), closing
the last interim-scope note the campaign-pack loader left standing —
`campaign.md`'s own `maturity_tier` field is a *reference* to a
separate tier file (id/display_name/rank front matter, real
prompt-constraint text as the body), not a prompt string `campaign.md`
carries directly, and nothing resolved that reference until now. A new
`internal/maturitytiers` package parses a tier directory the same
markdown-plus-front-matter shape `internal/campaignpack` already uses —
factored the actual front-matter-plus-body split out into a small new
`internal/frontmatter` package once both needed the identical logic,
rather than duplicating it. A new `-maturity-tiers-dir` flag (mirroring
`-campaign-policies`/`-room-passwords`'s own "host-authored, trusted,
flag-loaded" shape — this is host config, not something the admin panel
edits live) loads the registry once at startup;
`admin.CampaignPackPolicyProvider` — the same provider already
resolving `pvp_policy` from a bound pack — now resolves `maturity_tier`
against it too, independently: an unresolvable tier id (no registry
configured, or an id not in it) doesn't block a real `pvp_policy` from
still applying, and vice versa. Caught and fixed a real, pre-existing
bug from the campaign-pack pass along the way: the provider used to
return a bare `CampaignPolicy{PvPPolicy: ...}` on a pack match, silently
discarding whatever `PvPConsent`/`ImageMaturityTierPrompt`/
`PriceMultiplier` the admin-panel fallback had set — it now starts from
the fallback's own resolved policy and only overrides the fields the
pack actually has an opinion on.

Ships three real, original example tiers
(`maturity-tiers/family_friendly.md`/`standard.md`/`mature.md`,
matching design doc §6.5's own example filenames) — written to stay
unambiguously on the safe side of CLAUDE.md's content-maturity rule at
every rank, `mature` included, which says so explicitly in its own
body text. `rank`'s other named purpose — sanity-checking that an
image-gen tier override isn't set more permissive than the text tier —
is parsed and available but not yet enforced anywhere: nothing in this
codebase resolves an *image* maturity tier from a tier id the way
`maturity_tier` now resolves a text one, so there's no second rank to
compare against yet. A real, named, separate follow-on.

**Verified live**: real sidecar + Master + `qwen3.8:27b`, the real
`sable-ravine` pack bound (`maturity_tier: standard`) and a scratch tier
registry containing a `standard` tier whose prompt text included a
distinctive, testable marker instruction. The DM's real narration
opened with that exact marker, unprompted — proving the resolved tier
text actually reached generation, not just that the resolution logic
was correct in isolation (which the deterministic
`CampaignPackPolicyProvider` tests already cover, including the
field-preservation fix above).

**New**: real mounts/carts/wagons/ships — off-site possessions' other
named half, alongside the stash tools above (design doc §6.4's "off-site
possessions (mounts, stashes)"). A vehicle is deliberately never a
character/creature record, even for an animal mount: `store.Vehicle`
only tracks who has it and where it is (id, name, type, and whether it's
currently traveling with the party or stabled at a specific location) —
a mount that needs real combat stats (AC, HP, speed) is still created as
an ordinary character via `create_npc`/`FromJson`, the same as any other
creature. Four new DM tools mirror the location/stash tools' own gate
shapes exactly: `list_vehicles` (full enumeration), `acquire_vehicle`
(creates a vehicle starting in the traveling-with-the-party state — the
same "the party has to be somewhere" gate `stash_item` already uses),
`stable_vehicle` (real rejection if the vehicle is already stabled
somewhere else — take it from there first), and `take_vehicle` (real
rejection unless it's stabled at the party's *current* location). A
`travel_to` call needs no vehicle-specific handling at all: a
non-stabled vehicle has no location field to update, so it's simply
"with the party" by construction.

You separately asked for player-initiated vehicle import, not just the
DM's own `acquire_vehicle` — a new `vehicle.import` protocol message
(design doc §6.4) lets a player declare a new vehicle directly,
independent of the narrative tool loop entirely: no mechanical schema
the way `character.upload` has (a vehicle carries no system-engine
schema at all), just a real "name and vehicle_type aren't blank" check.
Both creation paths — a real `vehicle.import` and the DM's own
`acquire_vehicle` — broadcast the same `vehicle.imported` message to the
whole campaign through one shared helper, so every client learns about
a new shared vehicle identically regardless of which path created it.
`protocol/asyncapi.yaml` gained the matching `VehicleImport`/
`VehicleImported` message components.

**Verified live**: real sidecar + Master + `qwen3.8:27b`. Asked to buy a
mount at Keep Stonewatch, the DM called `list_vehicles` (confirming none
existed yet), then `acquire_vehicle`, and the real `vehicle.imported`
broadcast carried the correct name/type and a real generated id — all
before narrating the purchase. Asked to stable it there, `stable_vehicle`
succeeded and the underlying SQLite row confirmed `stabled = 1,
location_id = keep-stonewatch`. A direct `vehicle.import` sent as a raw
protocol message (bypassing the DM/LLM path entirely — no narrative
input at all) correctly created a second vehicle and broadcast
`vehicle.imported` on its own, confirmed against the same database
alongside the first.

**Verified live**: the admin API round-trip above (create a named
campaign, list it back with real defaults, archive it, confirm a player
can still join) ran against a real Master process with a real
file-backed SQLite database, not a mock. The turn-order/combat-map
"survives a restart" proof runs as a real Go test
(`TestCombatState_SecondServerSharingStore_RehydratesAndCanAdvanceTurn`)
rather than a full production restart: a second `*server.Server`,
sharing the same real `SQLiteEventStore` as the first but with its own
fresh, empty in-memory maps — exactly what a freshly-started Master
process would have — calls `WarmUpCombatState` and is then able to
advance the turn the first server left in progress, the same real
SQLite reads/writes a file-backed restart would exercise (`:memory:`
only changes connection pooling, not read/write correctness). Both new
store tables and every new admin API endpoint have their own
deterministic test coverage in `internal/store` and `internal/admin`.

The rest of §9 (§9.4's review panel, §9.6 spotlight balance, §9.7
knowledge scoping) is still to come — see
[`docs/design.md`](../docs/design.md) §3, §5, and §7–§10.

## Layout

```
main.go                      entrypoint: flag parsing, event store, LLM
                              provider wiring, listener, graceful shutdown
internal/protocol/           wire types for protocol/asyncapi.yaml (Envelope,
                              Message[T], per-message payloads) — no transport logic
internal/server/              WebSocket endpoint: the handshake, the
                              post-handshake read/dispatch loop, and
                              best-effort event persistence via internal/store
internal/session/              connection registry (design doc §3.1's "session
                              orchestration"): which clients are connected to
                              which campaign, and broadcasting to all of them
internal/store/                repository/DAO abstraction over storage (design
                              doc §10): EventStore + CharacterStore interfaces,
                              both implemented by SQLiteEventStore, the
                              zero-config default (pure-Go driver, no cgo)
internal/frontmatter/          shared markdown + YAML front-matter parser
                              (design doc §6.4/§6.5) — used by both
                              internal/campaignpack and internal/maturitytiers
internal/campaignpack/         parses a campaign pack directory (design
                              doc §6.4) — campaign.md/locations/npcs/encounters,
                              markdown + YAML front matter — into structured data;
                              mutable session state lives in internal/store instead
internal/maturitytiers/        parses a maturity-tier definitions directory
                              (design doc §6.5) — one *.md file per tier
                              (id/display_name/rank + prompt-constraint text)
internal/llm/                  LLM-provider contract (design doc §3.1) +
                              OllamaProvider, the first implementation
internal/auth/                  join-authorization contract (design doc §6.6) +
                              RoomPasswordProvider, the first implementation —
                              the seam a future Discord OAuth provider plugs into
internal/systemengine/        dials a System Engine gRPC sidecar (design doc
                              §6.1) — thin wrapper around the generated client,
                              no redundant interface on top of it
internal/systemenginepb/      generated gRPC/protobuf stubs for
                              protocol/system_engine.proto (gitignored;
                              regenerate with protocol/generate.sh) plus a
                              hand-written round-trip test
internal/admin/                the local-only admin/operator settings panel's
                              JSON API (design doc §3.3) — Campaign/Security
                              tab providers that wrap the JSON-file-loaded
                              auth/policy providers as a fallback, plus the
                              System tab and restart trigger
web/                          the V1 web client (design doc §4) — plain
                              HTML/CSS/JS, no build step, served by Master
                              itself from disk (not embedded — see below)
admin-web/                    the admin panel's own web UI — same plain
                              HTML/CSS/JS, no build step, but served from a
                              completely separate listener (-admin-addr)
```

## Prerequisites

- **Go 1.24+** (see `go.mod`) — the only hard requirement; Master compiles
  to a single static binary with no other runtime dependency (design doc
  §3.2).
- **The generated System Engine gRPC stubs must exist before this module
  will build at all.** `internal/systemenginepb/*.pb.go` is gitignored
  (not checked into this repo), but `main.go` and `internal/server`
  import that package unconditionally — a fresh clone will fail to
  compile until you run `../protocol/generate.sh` once from the repo
  root. That script itself needs `protoc` plus the
  `protoc-gen-go`/`protoc-gen-go-grpc` plugins on `PATH`; it prints exact
  install commands if any are missing.
- Everything else below is optional and only needed for the feature it
  powers — Master runs and serves the web client with none of them
  configured:
  - An [OpenCombatEngine](https://github.com/jamesplotts/opencombatengine)
    `GrpcSidecar` instance (a .NET/C# process, built separately from this
    repo) for real dice/rules resolution and character import
    (`-system-engine-addr`).
  - An [Ollama](https://ollama.com) server reachable over HTTP for
    AI-narrated responses (`-llm-url`).
  - A self-hosted [ComfyUI](https://github.com/comfyanonymous/ComfyUI)
    instance for the DM's `generate_scene_image` tool (`-comfyui-url`).
  - No Node/npm, no bundler for either web client (`web/`,
    `admin-web/`) — both are hand-written HTML/CSS/JS with no build step.

## Running

```
go run . -addr :8080 -db layforge.db -llm-url http://<ollama-host>:11434 -llm-model qwen3.8:27b
```

then open `http://localhost:8080/` — Master serves [`web/`](web/) at `/`
by default (`-web-dir` defaults to a `web` directory next to the running
binary, resolved from the executable's own path so this works regardless
of where you launch it from; pass `-web-dir=""` to disable). It's served
from plain files on disk rather than embedded into the binary
specifically so a table can restyle the interface — swap `web/style.css`,
fork `web/index.html`/`app.js` — without touching Go or rebuilding
anything; see `web/README.md`. That does mean distributing "the compiled
binary + its `web/` folder" together, not a single file — a completely
normal pattern for self-hosted software, and the tradeoff that buys the
reskinning. Serving Master's own client this way doesn't compromise the
protocol's openness (design doc §4): any other client is equally free to
connect to `/ws` directly, whatever Master happens to serve at `/`.

`-db` defaults to `layforge.db` in the working directory — SQLite,
zero-config, created on first run. Every message the WebSocket endpoint
exchanges is appended to its `events` table, scoped by `campaign_id` and
ordered by a store-assigned sequence number; inspect it directly with
`sqlite3 layforge.db`.

`-llm-url` has no default — narrative rendering is disabled (a
`narrative.player_input` gets a `system.error` explaining why) unless you
point it at a reachable Ollama server. `-llm-model` defaults to
`qwen3.8:27b`; pick whatever model your Ollama instance actually has
(`curl <url>/api/tags` to check). Not every model behaves well here — in
testing, a 7B "instruct" model produced reliably garbled output on
RPG-narrative-style prompts while a 27B model handled the same prompts
correctly; if narrative bubbles come back corrupted, try a different/
larger model before assuming the pipeline itself is broken. Model choice
matters even more for the DM tool-use loop (design doc §8) specifically
than for plain narration — see the DM tool-use section above for a
direct, live A/B comparison between `qwen2.5:32b` and this flag's actual
default, `qwen3.8:27b`, on the identical scenario.

`-room-passwords` points at a JSON file mapping `campaign_id` to a
required join password, e.g. `{"my-campaign": "hunter2"}` — a campaign
not listed stays open to anyone, so protecting one is opt-in per
campaign, not a server-wide switch. Leave it unset (the default) to
require no password anywhere. A missing or malformed file fails Master's
startup outright rather than silently running unprotected — a
self-hoster who asked for this shouldn't lose it to a typo without
noticing.

`-system-engine-addr` points at a running System Engine gRPC sidecar's
`host:port` (e.g. `localhost:5265` for OpenCombatEngine.GrpcSidecar run
locally). Leave it unset (the default) to run without rules
resolution/character import. grpc-go dials lazily, so Master makes one
real `GetCharacterSchema` call at startup to actually confirm
reachability — an unreachable or not-yet-started sidecar logs a warning
and Master still starts normally, the same way a missing `-web-dir` does.

`-admin-addr` (default `127.0.0.1:8090`) opens the admin/operator
settings panel described in the Status section above — open
`http://127.0.0.1:8090/` (or wherever you pointed it) locally on the same
machine Master is running on. It's deliberately not meant to be reachable
any other way: there's no login of its own, only the bind address stands
between it and anyone who can reach it, so **never** reverse-proxy or
otherwise expose this listener the way you might the main one. Pass
`-admin-addr=""` to disable it entirely. `-admin-web-dir` mirrors
`-web-dir`'s own reasoning (a directory next to the binary, not the
current working directory); leave it pointed at [`admin-web/`](admin-web/)
unless you're restyling that UI too.

## Testing

```
go test ./...
```

See [`CLAUDE.md`](../CLAUDE.md) for this repo's coding conventions — the
Go translation of design doc §12's AI directives (mandatory doc comments,
TDD, enum-sentinel pattern, explicit error handling, file headers).
