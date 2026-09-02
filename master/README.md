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
usable client to try all of this from — see `-web-dir` below and
[`clients/web/`](../clients/web/) — verified in an actual browser, not
just against hand-written test clients. The generated System Engine gRPC
client/server stubs build and round-trip correctly, but nothing in
`main.go` dials a real OpenCombatEngine sidecar yet. Every other message
category (roll, map, character, tool), the turn-order state machine,
authoritative dice, the narrative-transform pipeline's slow pass, and
governance gates beyond safety.flag are all still to come — see
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
                              doc §10): EventStore interface + SQLiteEventStore,
                              the zero-config default (pure-Go driver, no cgo)
internal/llm/                  LLM-provider contract (design doc §3.1) +
                              OllamaProvider, the first implementation
internal/systemenginepb/      generated gRPC/protobuf stubs for
                              protocol/system_engine.proto (gitignored;
                              regenerate with protocol/generate.sh) plus a
                              hand-written round-trip test
```

## Running

```
go run . -addr :8080 -db layforge.db -llm-url http://<ollama-host>:11434 -llm-model qwen3.8:27b -web-dir ../clients/web
```

then open `http://localhost:8080/` for the [V1 web client](../clients/web/)
(`-web-dir` is a dev convenience, off by default — see that flag's help
text and `clients/web/README.md` for why Master serving its own client is
not the intended production deployment shape).

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

## Testing

```
go test ./...
```

See [`CLAUDE.md`](../CLAUDE.md) for this repo's coding conventions — the
Go translation of design doc §12's AI directives (mandatory doc comments,
TDD, enum-sentinel pattern, explicit error handling, file headers).
