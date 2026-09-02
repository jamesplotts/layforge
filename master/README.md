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

Every other message category (map, tool), the turn-order state machine,
the narrative-transform pipeline's slow pass, and governance gates beyond
safety.flag are all still to come — see
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
