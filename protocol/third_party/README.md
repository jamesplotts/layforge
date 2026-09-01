# third_party

Vendored `.proto` sources this repo's own contracts import but doesn't
own. Copied verbatim (not modified) from upstream, license and copyright
headers intact.

- `google/protobuf/struct.proto`, `google/protobuf/timestamp.proto` —
  from Google's [protobuf](https://github.com/protocolbuffers/protobuf)
  project (BSD-3-Clause), pulled from Debian's `libprotobuf-dev` package
  (protobuf 3.21.12). `system_engine.proto` imports these for
  `google.protobuf.Struct` and `google.protobuf.Timestamp`.

  Vendored here rather than requiring every contributor to install
  `libprotobuf-dev` (or equivalent) just to get two small, stable files
  onto `protoc`'s include path — `protocol/generate.sh` points
  `--proto_path` at this directory. Update by re-copying from a newer
  `libprotobuf-dev`/protobuf release if these ever change upstream; they
  rarely do.
