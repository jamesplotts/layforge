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

## Go codegen for the System Engine contract

[`generate.sh`](generate.sh) regenerates the Go client/server stubs for
`system_engine.proto` directly into
`master/internal/systemenginepb/` — inside the Master module's own tree,
not a repo-level `gen/`, because a Go module can't cleanly import
generated code living outside its own directory tree without
workspace/multi-module machinery this project doesn't need with only one
Go consumer so far. If a second Go consumer shows up later, revisit this
(a `go.work` workspace tying multiple modules together is the natural
next step).

Requires on `PATH`: `protoc`, and the Go plugins (`go install
google.golang.org/protobuf/cmd/protoc-gen-go@latest` and `go install
google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`, then make sure
`$(go env GOPATH)/bin` is on `PATH`). Run it from the repo root:

```
./protocol/generate.sh
```

The well-known-types (`google.protobuf.Struct`, `google.protobuf.Timestamp`)
this contract imports are vendored under
[`third_party/`](third_party/) rather than requiring every contributor to
install `libprotobuf-dev` — see that directory's README.

Generated `*.pb.go` files are gitignored (`**/*.pb.go`) — regenerate,
don't commit. A future non-Go reference SDK (Python, per design doc §6)
would get its own generation step and its own output location, following
that language's own conventions rather than this one.
