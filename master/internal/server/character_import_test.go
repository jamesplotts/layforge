// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// fakeSystemEngineClient is a minimal systemenginepb.SystemEngineClient
// for testing character.upload, roll.check_request, character.
// schema_request, character.get, and character.apply_effect dispatch
// without a real gRPC sidecar. FromJson/ResolveCheck/
// GetCharacterSchema/GetCharacterStatus/ApplyEffect are configurable;
// every other method returns an error, since nothing dispatched in
// package server calls them yet.
type fakeSystemEngineClient struct {
	fromJsonResp *systemenginepb.FromJsonResponse
	fromJsonErr  error
	// lastFromJsonRequest captures the most recent FromJson() call's
	// request, for asserting on what Server actually sent the engine.
	lastFromJsonRequest *systemenginepb.FromJsonRequest

	resolveCheckResp *systemenginepb.ResolveCheckResponse
	resolveCheckErr  error
	// lastResolveCheckRequest captures the most recent ResolveCheck()
	// call's request, for asserting on what Server actually sent the
	// engine.
	lastResolveCheckRequest *systemenginepb.ResolveCheckRequest

	getCharacterSchemaResp *systemenginepb.GetCharacterSchemaResponse
	getCharacterSchemaErr  error

	getCharacterStatusResp *systemenginepb.GetCharacterStatusResponse
	getCharacterStatusErr  error
	// lastGetCharacterStatusRequest captures the most recent
	// GetCharacterStatus() call's request, for asserting on what Server
	// actually sent the engine.
	lastGetCharacterStatusRequest *systemenginepb.GetCharacterStatusRequest

	applyEffectResp *systemenginepb.ApplyEffectResponse
	applyEffectErr  error
	// lastApplyEffectRequest captures the most recent ApplyEffect() call's
	// request, for asserting on what Server actually sent the engine.
	lastApplyEffectRequest *systemenginepb.ApplyEffectRequest
}

func (f *fakeSystemEngineClient) FromJson(_ context.Context, in *systemenginepb.FromJsonRequest, _ ...grpc.CallOption) (*systemenginepb.FromJsonResponse, error) {
	f.lastFromJsonRequest = in
	if f.fromJsonErr != nil {
		return nil, f.fromJsonErr
	}
	return f.fromJsonResp, nil
}

func (f *fakeSystemEngineClient) ResolveCheck(_ context.Context, in *systemenginepb.ResolveCheckRequest, _ ...grpc.CallOption) (*systemenginepb.ResolveCheckResponse, error) {
	f.lastResolveCheckRequest = in
	if f.resolveCheckErr != nil {
		return nil, f.resolveCheckErr
	}
	return f.resolveCheckResp, nil
}

func (f *fakeSystemEngineClient) ApplyEffect(_ context.Context, in *systemenginepb.ApplyEffectRequest, _ ...grpc.CallOption) (*systemenginepb.ApplyEffectResponse, error) {
	f.lastApplyEffectRequest = in
	if f.applyEffectErr != nil {
		return nil, f.applyEffectErr
	}
	return f.applyEffectResp, nil
}

func (f *fakeSystemEngineClient) GetCharacterSchema(context.Context, *systemenginepb.GetCharacterSchemaRequest, ...grpc.CallOption) (*systemenginepb.GetCharacterSchemaResponse, error) {
	if f.getCharacterSchemaErr != nil {
		return nil, f.getCharacterSchemaErr
	}
	return f.getCharacterSchemaResp, nil
}

func (f *fakeSystemEngineClient) GetCharacterStatus(_ context.Context, in *systemenginepb.GetCharacterStatusRequest, _ ...grpc.CallOption) (*systemenginepb.GetCharacterStatusResponse, error) {
	f.lastGetCharacterStatusRequest = in
	if f.getCharacterStatusErr != nil {
		return nil, f.getCharacterStatusErr
	}
	return f.getCharacterStatusResp, nil
}

func (f *fakeSystemEngineClient) ValidateCharacter(context.Context, *systemenginepb.ValidateCharacterRequest, ...grpc.CallOption) (*systemenginepb.ValidateCharacterResponse, error) {
	return nil, errors.New("fakeSystemEngineClient: ValidateCharacter not implemented in this fake")
}

func (f *fakeSystemEngineClient) ToJson(context.Context, *systemenginepb.ToJsonRequest, ...grpc.CallOption) (*systemenginepb.ToJsonResponse, error) {
	return nil, errors.New("fakeSystemEngineClient: ToJson not implemented in this fake")
}

func (f *fakeSystemEngineClient) StreamEvents(context.Context, *systemenginepb.StreamEventsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[systemenginepb.EngineEvent], error) {
	return nil, errors.New("fakeSystemEngineClient: StreamEvents not implemented in this fake")
}

// newTestServerWithSystemEngine builds a Server with a real in-memory
// SQLite store (satisfying both store.EventStore and store.CharacterStore,
// same as production — see SQLiteEventStore's doc comment) and engine
// wired to fakeEngine, so character.upload and roll.check_request
// dispatch can be exercised end-to-end without a real gRPC sidecar.
func newTestServerWithSystemEngine(t *testing.T, fakeEngine *fakeSystemEngineClient) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, fakeEngine, st).Handler())
	return ts, st
}

// uploadCharacter sends a character.upload and returns either the
// character.validation_result payload or an error built from a
// system.error response — mirrors requestHistory's pattern in
// server_test.go for the same reason: a plain wsjson.Read into the
// success payload type would silently zero-fill on a system.error
// response instead of surfacing it.
func uploadCharacter(ctx context.Context, conn *websocket.Conn, campaignID, sender, characterJSON, schemaVersion string) (protocol.CharacterValidationResultPayload, error) {
	msg := protocol.CharacterUploadMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "upload-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterUpload,
		},
		Payload: protocol.CharacterUploadPayload{CharacterJSON: characterJSON, SchemaVersion: schemaVersion},
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return protocol.CharacterValidationResultPayload{}, err
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return protocol.CharacterValidationResultPayload{}, err
	}

	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return protocol.CharacterValidationResultPayload{}, fmt.Errorf("unmarshaling envelope: %w", err)
	}
	if envelope.Type == protocol.MessageTypeSystemError {
		var errMsg protocol.SystemErrorMessage
		if err := json.Unmarshal(data, &errMsg); err != nil {
			return protocol.CharacterValidationResultPayload{}, fmt.Errorf("unmarshaling system.error: %w", err)
		}
		return protocol.CharacterValidationResultPayload{}, fmt.Errorf("server rejected upload: %s", errMsg.Payload.Message)
	}

	var resp protocol.CharacterValidationResultMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return protocol.CharacterValidationResultPayload{}, fmt.Errorf("unmarshaling character.validation_result: %w", err)
	}
	return resp.Payload, nil
}

func TestServe_CharacterUpload_ValidCharacter_SavesAndRespondsWithValidationResult(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{
				ActorId:       "engine-actor-1",
				CharacterData: characterData,
				SchemaVersion: "opencombatengine-v1",
			},
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-import", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-import", "player-a", `{"name":"Kestrel"}`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}
	if payload.CharacterID == "" {
		t.Fatal("Payload.CharacterID is empty, want a generated id")
	}
	if len(payload.Warnings) != 0 {
		t.Errorf("Payload.Warnings = %v, want none", payload.Warnings)
	}

	if fake.lastFromJsonRequest.Json != `{"name":"Kestrel"}` {
		t.Errorf("FromJson called with Json = %q, want the uploaded character_json", fake.lastFromJsonRequest.Json)
	}

	saved, err := st.GetCharacter(ctx, payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.CampaignID != "campaign-import" {
		t.Errorf("saved.CampaignID = %q, want %q", saved.CampaignID, "campaign-import")
	}
	if saved.OwnerID != "player-a" {
		t.Errorf("saved.OwnerID = %q, want %q", saved.OwnerID, "player-a")
	}
	if saved.SchemaVersion != "opencombatengine-v1" {
		t.Errorf("saved.SchemaVersion = %q, want %q", saved.SchemaVersion, "opencombatengine-v1")
	}
	if saved.Status != store.CharacterStatusPendingReview {
		t.Errorf("saved.Status = %q, want %q (no reviewer concept exists yet)", saved.Status, store.CharacterStatusPendingReview)
	}
}

func TestServe_CharacterUpload_EngineCannotParse_RespondsWithErrorWarningAndNoCharacterID(t *testing.T) {
	fake := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Warnings: []*systemenginepb.ValidationWarning{
				{FieldPath: "", Message: "malformed JSON", Severity: "error"},
			},
		},
	}
	ts, _ := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-import", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, err := uploadCharacter(ctx, conn, "campaign-import", "player-a", `{not valid`, "opencombatengine-v1")
	if err != nil {
		t.Fatalf("uploadCharacter() error = %v", err)
	}
	if payload.CharacterID != "" {
		t.Errorf("Payload.CharacterID = %q, want empty (nothing should be saved)", payload.CharacterID)
	}
	if len(payload.Warnings) != 1 || payload.Warnings[0].Severity != "error" {
		t.Errorf("Payload.Warnings = %v, want one error-severity warning", payload.Warnings)
	}
}

func TestServe_CharacterUpload_NoSystemEngineConfigured_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t) // no system engine, no character store
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := protocol.CharacterUploadMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "upload-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-1",
			Type:            protocol.MessageTypeCharacterUpload,
		},
		Payload: protocol.CharacterUploadPayload{CharacterJSON: `{}`, SchemaVersion: "v1"},
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.InReplyToMessageID != msg.MessageID {
		t.Errorf("Payload.InReplyToMessageID = %q, want %q", got.Payload.InReplyToMessageID, msg.MessageID)
	}
}

func TestServe_CharacterUpload_EngineCallFails_RespondsWithErrorAndKeepsConnectionOpen(t *testing.T) {
	fake := &fakeSystemEngineClient{fromJsonErr: errors.New("sidecar unreachable")}
	ts, _ := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-import", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := uploadCharacter(ctx, conn, "campaign-import", "player-a", `{}`, "v1"); err == nil {
		t.Fatal("uploadCharacter() error = nil, want a rejection from the engine call failing")
	}

	// Connection must still be usable afterward — an engine failure is a
	// per-request error, not a reason to drop the client (same pattern as
	// narrative.player_input's provider-error test).
	flag := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-after-engine-error",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-import",
			Type:            protocol.MessageTypeSafetyFlag,
		},
	}
	if err := wsjson.Write(ctx, conn, flag); err != nil {
		t.Fatalf("Write(safety.flag) after engine error error = %v", err)
	}
	var gotBroadcast protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &gotBroadcast); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) after engine error error = %v", err)
	}
}
