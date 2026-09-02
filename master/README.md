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
would. **A real limitation found via live testing:** a mid-size model's
first-attempt JSON often doesn't validate against OpenCombatEngine's
actual schema (e.g. an `id` field that isn't a well-formed value the
engine's deserializer accepts) — `create_npc` correctly rejects it
rather than silently accepting something malformed, and the system
prompt tells the model to acknowledge the failure narratively rather
than claim a monster/turn order exists that doesn't; it doesn't
currently retry with a corrected document within the same slow pass.
Also found live: an LLM occasionally emits a failed tool-call attempt as
plain narration text instead of populating its structured tool-call
field — `looksLikeMalformedToolCall` in `dm_slow_pass.go` catches the
common shapes and drops that turn's narration rather than broadcasting
the artifact to the table.

Two more governance gates now exist too (design doc §9.1, §9.5 — see
package `policy` and `campaignPolicy`/`withMaturityConstraint` in
`server.go`): PvP policy is a real mechanical gate — `dmApplyEffect`
blocks a hostile (damage) `apply_effect` against a *different* player's
own character outright unless the campaign's configured policy permits
it (`pve_only`/`pvp_allowed`/`pvp_with_consent`, checked against a
pre-declared consent list for the consent case), never left to the DM
model to self-police; healing another player, or any effect against an
NPC/monster or the acting player's own character, is unaffected. The
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

Every other message category (map) and the rest of §9 (§9.4's review
panel, §9.6 spotlight balance, §9.7 knowledge scoping) are still to come
— see [`docs/design.md`](../docs/design.md) §3, §5, and §7–§10.

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
web/                          the V1 web client (design doc §4) — plain
                              HTML/CSS/JS, no build step, served by Master
                              itself from disk (not embedded — see below)
```

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
larger model before assuming the pipeline itself is broken.

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

## Testing

```
go test ./...
```

See [`CLAUDE.md`](../CLAUDE.md) for this repo's coding conventions — the
Go translation of design doc §12's AI directives (mandatory doc comments,
TDD, enum-sentinel pattern, explicit error handling, file headers).
