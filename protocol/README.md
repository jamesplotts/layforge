# Protocol

Two contracts, matched to their audience (see [`docs/design.md`](../docs/design.md) §6):

- **Client-facing WebSocket protocol** — JSON messages, spec'd in AsyncAPI.
  Message categories: `narrative.*`, `map.*`, `roll.*`, `audio.*`,
  `character.*`, `safety.*`, `tool.*`, `system.*` (design doc §5).
- **System Engine contract** — `.proto` file, gRPC. `resolve_check`,
  `apply_effect`, `get_character_schema`, `get_character_status`,
  `validate_character`, `to_json`/`from_json` (design doc §6.1).

Not yet written. Generated stubs (any language) belong under `gen/`
(gitignored — regenerate via `protoc`, don't commit).
