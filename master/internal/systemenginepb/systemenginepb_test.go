// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.
//
// This file is hand-written, unlike its siblings in this directory —
// protocol/generate.sh only ever (re)writes system_engine.pb.go and
// system_engine_grpc.pb.go, so this test is safe to keep here rather
// than in a separate package. It exists to verify the generated
// client/server pair actually works over real gRPC, not just that it
// compiles.

package systemenginepb_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// fakeEngine is a minimal SystemEngine implementation for testing the
// generated stubs, not a real system engine.
type fakeEngine struct {
	pb.UnimplementedSystemEngineServer
}

func (fakeEngine) GetCharacterSchema(context.Context, *pb.GetCharacterSchemaRequest) (*pb.GetCharacterSchemaResponse, error) {
	return &pb.GetCharacterSchemaResponse{
		SchemaVersion: "v1",
		JsonSchema:    `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`,
	}, nil
}

// TestSystemEngineClient_GetCharacterSchema_RoundTripsOverGRPC dials a
// fake SystemEngine server over an in-process bufconn listener (no real
// network port needed) and confirms a real gRPC call — request
// marshaled, sent, received, response unmarshaled — comes back correctly.
// This is the thing "wiring up the codegen" actually needs to prove: not
// just that the generated Go compiles, but that a generated client can
// talk to a generated server.
func TestSystemEngineClient_GetCharacterSchema_RoundTripsOverGRPC(t *testing.T) {
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	t.Cleanup(func() { _ = lis.Close() })

	grpcServer := grpc.NewServer()
	pb.RegisterSystemEngineServer(grpcServer, fakeEngine{})
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewSystemEngineClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := client.GetCharacterSchema(ctx, &pb.GetCharacterSchemaRequest{})
	if err != nil {
		t.Fatalf("GetCharacterSchema() error = %v", err)
	}
	if got.GetSchemaVersion() != "v1" {
		t.Errorf("SchemaVersion = %q, want %q", got.GetSchemaVersion(), "v1")
	}
	if got.GetJsonSchema() == "" {
		t.Error("JsonSchema is empty, want the fake schema document")
	}
}
