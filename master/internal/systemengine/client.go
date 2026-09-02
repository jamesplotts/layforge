// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package systemengine dials the System Engine gRPC contract
// (docs/design.md §6.1, protocol/system_engine.proto) that Master calls
// for all mechanical rules resolution. It deliberately does not define
// its own client interface: the generated systemenginepb.SystemEngineClient
// already is that interface at the point of consumption (CLAUDE.md's Go
// interface-design conventions), and Master must call the engine only
// through the gRPC contract, never a language-specific shortcut — wrapping
// it in a second, redundant interface here would just be indirection.
package systemengine

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// Dial connects to a System Engine gRPC sidecar (e.g. OpenCombatEngine's
// GrpcSidecar) at addr, a bare host:port such as "localhost:5265", and
// returns a client for it. The connection uses insecure transport
// credentials: a system-engine sidecar is a loopback/local process Master
// spawns or is configured to reach, not a public endpoint (design doc
// §6.1) — matching the reference sidecar's own choice to run cleartext
// HTTP/2 rather than TLS. The returned close function shuts the
// connection down; callers should defer it.
//
// grpc-go's client connects lazily, so a non-nil error here only happens
// for a malformed target string — confirmed empirically, not assumed: an
// address nothing is listening on, or one describing an unreachable host,
// dials successfully and only fails once the first RPC is attempted.
// Callers that want to fail fast on a misconfigured or unreachable sidecar
// (e.g. Master's own startup) need to make a real call, not just check
// this error.
func Dial(addr string) (systemenginepb.SystemEngineClient, func() error, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dialing system engine at %s: %w", addr, err)
	}
	return systemenginepb.NewSystemEngineClient(conn), conn.Close, nil
}
