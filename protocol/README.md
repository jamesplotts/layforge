# Protocol

Two contracts, matched to their audience (see [`docs/design.md`](../docs/design.md) §6):

- [`asyncapi.yaml`](asyncapi.yaml) — **client-facing WebSocket protocol**,
  JSON messages spec'd in AsyncAPI 2.6. Message categories: `narrative.*`,
  `map.*`, `roll.*`, `audio.*`, `character.*`, `safety.*`, `tool.*`,
  `system.*` (design doc §5). First draft: a representative message or two
  per category, not an exhaustive enumeration — extend it as each message
  is actually implemented. Validate with
  `npx @asyncapi/cli validate protocol/asyncapi.yaml`.
- [`system_engine.proto`](system_engine.proto) — **System Engine
  contract**, gRPC/protobuf. `ResolveCheck`, `ApplyEffect`,
  `GetCharacterSchema`, `GetCharacterStatus`, `ValidateCharacter`,
  `ToJson`/`FromJson`, plus a `StreamEvents` server-streaming feed for the
  sidecar to relay OpenCombatEngine's internal combat events across the
  process boundary (design doc §6.1, §12).

Neither contract has a Go/TS/Python codegen step wired up yet — that's
next. Generated stubs (any language) belong under `gen/` (gitignored —
regenerate via `protoc`/AsyncAPI generator, don't commit).
