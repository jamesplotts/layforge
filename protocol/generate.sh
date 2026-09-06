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

missing=()
for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		missing+=("$tool")
	fi
done

if [ "${#missing[@]}" -gt 0 ]; then
	echo "generate.sh: required tool(s) not found on PATH: ${missing[*]}" >&2
	echo >&2
	for tool in "${missing[@]}"; do
		case "$tool" in
		protoc)
			echo "  protoc: apt install protobuf-compiler (or your platform's equivalent)" >&2
			;;
		protoc-gen-go)
			# Debian/Ubuntu do package this one (apt install protoc-gen-go),
			# unlike protoc-gen-go-grpc below — but 'go install' works
			# everywhere, so it's given as the one portable answer here.
			echo "  protoc-gen-go: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" >&2
			;;
		protoc-gen-go-grpc)
			echo "  protoc-gen-go-grpc: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" >&2
			;;
		esac
	done
	echo >&2
	echo "  After a 'go install', ensure \$(go env GOPATH)/bin is on your PATH — add" >&2
	echo "  'export PATH=\"\$PATH:\$(go env GOPATH)/bin\"' to your shell profile if it isn't." >&2
	exit 1
fi

# Debian/Ubuntu DO package protoc-gen-go-grpc (apt install protoc-gen-go-grpc)
# — but that package has been observed generating an older interface shape
# (a named SystemEngine_StreamEventsClient type) than this project's pinned
# grpc-go version (see master/go.mod) expects, which uses the newer generic
# streaming-client interfaces (grpc.ServerStreamingClient[T]) instead, and
# the symptom is a confusing internal/systemenginepb compile error minutes
# later, not an error here. Reproduced for real on a Debian trixie box with
# both an OS-packaged and a 'go install'ed copy on PATH — the OS one
# happened to resolve first. Rather than just warn about PATH order,
# explicitly point protoc at the 'go install'ed copy (via --plugin, which
# overrides protoc's own PATH-based plugin lookup) whenever one exists in
# $(go env GOPATH)/bin, regardless of which copy a bare PATH search would
# have found — this makes generation correct even when an OS package
# shadows the right binary, not just correct if the user's PATH happens to
# be ordered right today.
gopath_bin="$(go env GOPATH 2>/dev/null)/bin"
go_plugin="$(command -v protoc-gen-go)"
if [ -x "$gopath_bin/protoc-gen-go" ]; then
	go_plugin="$gopath_bin/protoc-gen-go"
fi
grpc_plugin="$(command -v protoc-gen-go-grpc)"
if [ -x "$gopath_bin/protoc-gen-go-grpc" ]; then
	grpc_plugin="$gopath_bin/protoc-gen-go-grpc"
fi

protoc \
	--proto_path=protocol \
	--proto_path=protocol/third_party \
	--plugin="protoc-gen-go=$go_plugin" \
	--plugin="protoc-gen-go-grpc=$grpc_plugin" \
	--go_out=master --go_opt=module=github.com/jamesplotts/layforge/master \
	--go-grpc_out=master --go-grpc_opt=module=github.com/jamesplotts/layforge/master \
	system_engine.proto

echo "generate.sh: wrote master/internal/systemenginepb/"
