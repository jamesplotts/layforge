# Master

The Master process (Go). Session orchestration, turn-order state machine,
authoritative dice, campaign state store, narrative-transform pipeline,
tool-use dispatch, governance gates, and the WebSocket server clients
connect to.

## Status

Only the client-handshake WebSocket endpoint exists so far:
`system.connect` in, `system.session_state` (`joined`) or `system.error`
out, per `protocol/asyncapi.yaml`. Session orchestration, the turn-order
state machine, authoritative dice, the narrative-transform pipeline, and
tool-use dispatch are all still to come — see
[`docs/design.md`](../docs/design.md) §3 and §7–§10.

## Layout

```
main.go                      entrypoint: flag parsing, listener, graceful shutdown
internal/protocol/           wire types for protocol/asyncapi.yaml (Envelope,
                              Message[T], per-message payloads) — no transport logic
internal/server/              WebSocket endpoint and the connection handshake
```

## Running

```
go run . -addr :8080
```

## Testing

```
go test ./...
```

See [`CLAUDE.md`](../CLAUDE.md) for this repo's coding conventions — the
Go translation of design doc §12's AI directives (mandatory doc comments,
TDD, enum-sentinel pattern, explicit error handling, file headers).
