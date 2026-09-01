#!/usr/bin/env bash
# Copyright (c) 2026 James Duane Plotts
# Licensed under the MIT License. See LICENSE in the repository root.
#
# Regenerates the Go stubs for protocol/system_engine.proto directly into
# the Master module (master/internal/systemenginepb/), per the go_package
# option in that file. Output there is gitignored (**/*.pb.go) — this
# script is what regenerates it; don't hand-edit the generated files.
#
# This does not clean the output directory first, so it's safe to keep
# hand-written files (e.g. tests) alongside the generated ones — but it
# also means a renamed/removed message won't clean up its old generated
# file automatically. Delete master/internal/systemenginepb/*.pb.go by
# hand if that ever happens.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "generate.sh: required tool '$tool' not found on PATH" >&2
		echo "  protoc: apt install protobuf-compiler (or your platform's equivalent)" >&2
		echo "  protoc-gen-go / protoc-gen-go-grpc: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" >&2
		echo "                                       go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" >&2
		echo "  (ensure \$(go env GOPATH)/bin is on your PATH)" >&2
		exit 1
	fi
done

protoc \
	--proto_path=protocol \
	--proto_path=protocol/third_party \
	--go_out=master --go_opt=module=github.com/jamesplotts/layforge/master \
	--go-grpc_out=master --go-grpc_opt=module=github.com/jamesplotts/layforge/master \
	system_engine.proto

echo "generate.sh: wrote master/internal/systemenginepb/"
