// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package systemengine

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// fakeEngineServer answers GetCharacterSchema with a canned response and
// embeds UnimplementedSystemEngineServer for every other method, so a real
// grpc.Server can serve it without implementing the full contract — this
// test only needs to prove Dial produces a client that can complete a real
// round trip against a real listener.
type fakeEngineServer struct {
	systemenginepb.UnimplementedSystemEngineServer
}

func (fakeEngineServer) GetCharacterSchema(context.Context, *systemenginepb.GetCharacterSchemaRequest) (*systemenginepb.GetCharacterSchemaResponse, error) {
	return &systemenginepb.GetCharacterSchemaResponse{SchemaVersion: "fake-v1", JsonSchema: "{}"}, nil
}

// startFakeEngine starts a real gRPC server on a real loopback listener and
// returns its address; the server is stopped via t.Cleanup.
func startFakeEngine(t *testing.T) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	srv := grpc.NewServer()
	systemenginepb.RegisterSystemEngineServer(srv, fakeEngineServer{})

	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)

	return lis.Addr().String()
}

func TestDial_RealServer_CompletesRealRoundTrip(t *testing.T) {
	addr := startFakeEngine(t)

	client, closeFn, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.GetCharacterSchema(ctx, &systemenginepb.GetCharacterSchemaRequest{})
	if err != nil {
		t.Fatalf("GetCharacterSchema: %v", err)
	}
	if resp.SchemaVersion != "fake-v1" {
		t.Errorf("SchemaVersion = %q, want %q", resp.SchemaVersion, "fake-v1")
	}
}

func TestDial_NothingListening_DialSucceedsButCallFails(t *testing.T) {
	// grpc.NewClient dials lazily: an unreachable-but-well-formed target
	// only fails once an RPC is actually attempted, not at Dial time. This
	// documents that behavior for callers (main.go's startup connectivity
	// check) that need to distinguish "misconfigured address" from
	// "sidecar isn't running yet".
	client, closeFn, err := Dial("127.0.0.1:1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.GetCharacterSchema(ctx, &systemenginepb.GetCharacterSchemaRequest{}); err == nil {
		t.Error("expected an error calling an RPC against nothing listening, got nil")
	}
}
