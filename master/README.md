# Master

The Master process (Go). Session orchestration, turn-order state machine,
authoritative dice, campaign state store, narrative-transform pipeline,
tool-use dispatch, governance gates, and the WebSocket server clients
connect to.

## Status

Multiple clients can now connect to the same campaign and actually see
each other: `system.connect` in, `system.session_state`/`system.error`
out, then the connection stays open and Master routes `safety.flag` from
any client to a `safety.flag_broadcast` delivered to every client in that
campaign (design doc §9.2) — the first real cross-client message routing,
via `internal/session`'s connection registry. Every message exchanged
(handshake, flags, and broadcasts) is durably logged to SQLite. The
generated System Engine gRPC client/server stubs build and round-trip
correctly, but nothing in `main.go` dials a real OpenCombatEngine sidecar
yet. Every other message category (narrative, roll, map, character,
tool), the turn-order state machine, authoritative dice, the
narrative-transform pipeline, and governance gates beyond safety.flag are
all still to come — see [`docs/design.md`](../docs/design.md) §3, §5, and
§7–§10.

## Layout

```
main.go                      entrypoint: flag parsing, event store, listener,
                              graceful shutdown
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
internal/systemenginepb/      generated gRPC/protobuf stubs for
                              protocol/system_engine.proto (gitignored;
                              regenerate with protocol/generate.sh) plus a
                              hand-written round-trip test
```

## Running

```
go run . -addr :8080 -db layforge.db
```

`-db` defaults to `layforge.db` in the working directory — SQLite,
zero-config, created on first run. Every message the WebSocket endpoint
exchanges during the handshake is appended to its `events` table,
scoped by `campaign_id` and ordered by a store-assigned sequence number;
inspect it directly with `sqlite3 layforge.db`.

## Testing

```
go test ./...
```

See [`CLAUDE.md`](../CLAUDE.md) for this repo's coding conventions — the
Go translation of design doc §12's AI directives (mandatory doc comments,
TDD, enum-sentinel pattern, explicit error handling, file headers).
