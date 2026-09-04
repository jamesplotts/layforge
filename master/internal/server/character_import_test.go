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
	// resolveCheckFunc, when set, computes the response per call instead
	// of the fixed resolveCheckResp/resolveCheckErr above — needed by
	// turn-order tests, where different characters must roll different
	// initiative totals in the same test.
	resolveCheckFunc func(*systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error)
	// lastResolveCheckRequest captures the most recent ResolveCheck()
	// call's request, for asserting on what Server actually sent the
	// engine.
	lastResolveCheckRequest *systemenginepb.ResolveCheckRequest
	// resolveCheckRequests captures every ResolveCheck() call's request,
	// in order — for tests that need to assert on more than just the
	// last one (e.g. initiative rolled for several characters).
	resolveCheckRequests []*systemenginepb.ResolveCheckRequest

	getCharacterSchemaResp *systemenginepb.GetCharacterSchemaResponse
	getCharacterSchemaErr  error

	getCharacterStatusResp *systemenginepb.GetCharacterStatusResponse
	getCharacterStatusErr  error
	// getCharacterStatusFunc, when set, computes the response per call
	// instead of the fixed getCharacterStatusResp/getCharacterStatusErr
	// above — needed by turn-order tests, where different characters must
	// report different statuses in the same test.
	getCharacterStatusFunc func(*systemenginepb.GetCharacterStatusRequest) (*systemenginepb.GetCharacterStatusResponse, error)
	// lastGetCharacterStatusRequest captures the most recent
	// GetCharacterStatus() call's request, for asserting on what Server
	// actually sent the engine.
	lastGetCharacterStatusRequest *systemenginepb.GetCharacterStatusRequest

	applyEffectResp *systemenginepb.ApplyEffectResponse
	applyEffectErr  error
	// lastApplyEffectRequest captures the most recent ApplyEffect() call's
	// request, for asserting on what Server actually sent the engine.
	lastApplyEffectRequest *systemenginepb.ApplyEffectRequest

	castSpellResp *systemenginepb.CastSpellResponse
	castSpellErr  error
	// castSpellFunc, when set, computes the response per call instead of
	// the fixed castSpellResp/castSpellErr above — needed by tests where
	// different spell names/targets in the same test must get different
	// results.
	castSpellFunc func(*systemenginepb.CastSpellRequest) (*systemenginepb.CastSpellResponse, error)
	// lastCastSpellRequest captures the most recent CastSpell() call's
	// request, for asserting on what Server actually sent the engine.
	lastCastSpellRequest *systemenginepb.CastSpellRequest

	startTurnResp *systemenginepb.StartTurnResponse
	startTurnErr  error
	// startTurnFunc, when set, computes the response per call instead of
	// the fixed startTurnResp/startTurnErr above — needed by tests where
	// different characters starting their turn must get different
	// results in the same test.
	startTurnFunc func(*systemenginepb.StartTurnRequest) (*systemenginepb.StartTurnResponse, error)
	// startTurnRequests captures every StartTurn() call's request, in
	// order — for tests asserting on which characters' turns actually
	// started and in what sequence.
	startTurnRequests []*systemenginepb.StartTurnRequest

	attackResp *systemenginepb.AttackResponse
	attackErr  error
	// attackFunc, when set, computes the response per call instead of the
	// fixed attackResp/attackErr above — needed by tests where different
	// attackers/kinds in the same test must get different results.
	attackFunc func(*systemenginepb.AttackRequest) (*systemenginepb.AttackResponse, error)
	// lastAttackRequest captures the most recent Attack() call's request,
	// for asserting on what Server actually sent the engine.
	lastAttackRequest *systemenginepb.AttackRequest

	getAvailableActionsResp *systemenginepb.GetAvailableActionsResponse
	getAvailableActionsErr  error
	// getAvailableActionsFunc, when set, computes the response per call
	// instead of the fixed getAvailableActionsResp/getAvailableActionsErr
	// above — needed by tests where different characters in the same
	// test must get different results (e.g. turn-order incapacitation
	// skipping).
	getAvailableActionsFunc func(*systemenginepb.GetAvailableActionsRequest) (*systemenginepb.GetAvailableActionsResponse, error)
	// lastGetAvailableActionsRequest captures the most recent
	// GetAvailableActions() call's request, for asserting on what Server
	// actually sent the engine.
	lastGetAvailableActionsRequest *systemenginepb.GetAvailableActionsRequest

	grappleResp *systemenginepb.GrappleResponse
	grappleErr  error
	// lastGrappleRequest captures the most recent Grapple() call's
	// request, for asserting on what Server actually sent the engine.
	lastGrappleRequest *systemenginepb.GrappleRequest

	shoveResp *systemenginepb.ShoveResponse
	shoveErr  error
	// lastShoveRequest captures the most recent Shove() call's request,
	// for asserting on what Server actually sent the engine.
	lastShoveRequest *systemenginepb.ShoveRequest

	equipItemResp *systemenginepb.EquipItemResponse
	equipItemErr  error
	// lastEquipItemRequest captures the most recent EquipItem() call's
	// request, for asserting on what Server actually sent the engine.
	lastEquipItemRequest *systemenginepb.EquipItemRequest

	unequipItemResp *systemenginepb.UnequipItemResponse
	unequipItemErr  error
	// lastUnequipItemRequest captures the most recent UnequipItem() call's
	// request, for asserting on what Server actually sent the engine.
	lastUnequipItemRequest *systemenginepb.UnequipItemRequest

	addItemToInventoryResp *systemenginepb.AddItemToInventoryResponse
	addItemToInventoryErr  error
	// lastAddItemToInventoryRequest captures the most recent
	// AddItemToInventory() call's request, for asserting on what Server
	// actually sent the engine.
	lastAddItemToInventoryRequest *systemenginepb.AddItemToInventoryRequest

	removeItemFromInventoryResp *systemenginepb.RemoveItemFromInventoryResponse
	removeItemFromInventoryErr  error
	// lastRemoveItemFromInventoryRequest captures the most recent
	// RemoveItemFromInventory() call's request, for asserting on what
	// Server actually sent the engine.
	lastRemoveItemFromInventoryRequest *systemenginepb.RemoveItemFromInventoryRequest

	transferItemResp *systemenginepb.TransferItemResponse
	transferItemErr  error
	// lastTransferItemRequest captures the most recent TransferItem()
	// call's request, for asserting on what Server actually sent the
	// engine.
	lastTransferItemRequest *systemenginepb.TransferItemRequest

	generateLootResp *systemenginepb.GenerateLootResponse
	generateLootErr  error
	// lastGenerateLootRequest captures the most recent GenerateLoot()
	// call's request, for asserting on what Server actually sent the
	// engine.
	lastGenerateLootRequest *systemenginepb.GenerateLootRequest

	addCurrencyResp *systemenginepb.AddCurrencyResponse
	addCurrencyErr  error
	// lastAddCurrencyRequest captures the most recent AddCurrency() call's
	// request, for asserting on what Server actually sent the engine.
	lastAddCurrencyRequest *systemenginepb.AddCurrencyRequest

	transferCurrencyResp *systemenginepb.TransferCurrencyResponse
	transferCurrencyErr  error
	// lastTransferCurrencyRequest captures the most recent
	// TransferCurrency() call's request, for asserting on what Server
	// actually sent the engine.
	lastTransferCurrencyRequest *systemenginepb.TransferCurrencyRequest

	getItemInfoResp *systemenginepb.GetItemInfoResponse
	getItemInfoErr  error
	// lastGetItemInfoRequest captures the most recent GetItemInfo() call's
	// request, for asserting on what Server actually sent the engine.
	lastGetItemInfoRequest *systemenginepb.GetItemInfoRequest

	listInventoryResp *systemenginepb.ListInventoryResponse
	listInventoryErr  error
	// lastListInventoryRequest captures the most recent ListInventory()
	// call's request, for asserting on what Server actually sent the
	// engine.
	lastListInventoryRequest *systemenginepb.ListInventoryRequest
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
	f.resolveCheckRequests = append(f.resolveCheckRequests, in)
	if f.resolveCheckFunc != nil {
		return f.resolveCheckFunc(in)
	}
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

func (f *fakeSystemEngineClient) CastSpell(_ context.Context, in *systemenginepb.CastSpellRequest, _ ...grpc.CallOption) (*systemenginepb.CastSpellResponse, error) {
	f.lastCastSpellRequest = in
	if f.castSpellFunc != nil {
		return f.castSpellFunc(in)
	}
	if f.castSpellErr != nil {
		return nil, f.castSpellErr
	}
	return f.castSpellResp, nil
}

func (f *fakeSystemEngineClient) Attack(_ context.Context, in *systemenginepb.AttackRequest, _ ...grpc.CallOption) (*systemenginepb.AttackResponse, error) {
	f.lastAttackRequest = in
	if f.attackFunc != nil {
		return f.attackFunc(in)
	}
	if f.attackErr != nil {
		return nil, f.attackErr
	}
	return f.attackResp, nil
}

func (f *fakeSystemEngineClient) Grapple(_ context.Context, in *systemenginepb.GrappleRequest, _ ...grpc.CallOption) (*systemenginepb.GrappleResponse, error) {
	f.lastGrappleRequest = in
	if f.grappleErr != nil {
		return nil, f.grappleErr
	}
	return f.grappleResp, nil
}

func (f *fakeSystemEngineClient) Shove(_ context.Context, in *systemenginepb.ShoveRequest, _ ...grpc.CallOption) (*systemenginepb.ShoveResponse, error) {
	f.lastShoveRequest = in
	if f.shoveErr != nil {
		return nil, f.shoveErr
	}
	return f.shoveResp, nil
}

func (f *fakeSystemEngineClient) EquipItem(_ context.Context, in *systemenginepb.EquipItemRequest, _ ...grpc.CallOption) (*systemenginepb.EquipItemResponse, error) {
	f.lastEquipItemRequest = in
	if f.equipItemErr != nil {
		return nil, f.equipItemErr
	}
	return f.equipItemResp, nil
}

func (f *fakeSystemEngineClient) UnequipItem(_ context.Context, in *systemenginepb.UnequipItemRequest, _ ...grpc.CallOption) (*systemenginepb.UnequipItemResponse, error) {
	f.lastUnequipItemRequest = in
	if f.unequipItemErr != nil {
		return nil, f.unequipItemErr
	}
	return f.unequipItemResp, nil
}

func (f *fakeSystemEngineClient) AddItemToInventory(_ context.Context, in *systemenginepb.AddItemToInventoryRequest, _ ...grpc.CallOption) (*systemenginepb.AddItemToInventoryResponse, error) {
	f.lastAddItemToInventoryRequest = in
	if f.addItemToInventoryErr != nil {
		return nil, f.addItemToInventoryErr
	}
	return f.addItemToInventoryResp, nil
}

func (f *fakeSystemEngineClient) RemoveItemFromInventory(_ context.Context, in *systemenginepb.RemoveItemFromInventoryRequest, _ ...grpc.CallOption) (*systemenginepb.RemoveItemFromInventoryResponse, error) {
	f.lastRemoveItemFromInventoryRequest = in
	if f.removeItemFromInventoryErr != nil {
		return nil, f.removeItemFromInventoryErr
	}
	return f.removeItemFromInventoryResp, nil
}

func (f *fakeSystemEngineClient) TransferItem(_ context.Context, in *systemenginepb.TransferItemRequest, _ ...grpc.CallOption) (*systemenginepb.TransferItemResponse, error) {
	f.lastTransferItemRequest = in
	if f.transferItemErr != nil {
		return nil, f.transferItemErr
	}
	return f.transferItemResp, nil
}

func (f *fakeSystemEngineClient) GenerateLoot(_ context.Context, in *systemenginepb.GenerateLootRequest, _ ...grpc.CallOption) (*systemenginepb.GenerateLootResponse, error) {
	f.lastGenerateLootRequest = in
	if f.generateLootErr != nil {
		return nil, f.generateLootErr
	}
	return f.generateLootResp, nil
}

func (f *fakeSystemEngineClient) AddCurrency(_ context.Context, in *systemenginepb.AddCurrencyRequest, _ ...grpc.CallOption) (*systemenginepb.AddCurrencyResponse, error) {
	f.lastAddCurrencyRequest = in
	if f.addCurrencyErr != nil {
		return nil, f.addCurrencyErr
	}
	return f.addCurrencyResp, nil
}

func (f *fakeSystemEngineClient) TransferCurrency(_ context.Context, in *systemenginepb.TransferCurrencyRequest, _ ...grpc.CallOption) (*systemenginepb.TransferCurrencyResponse, error) {
	f.lastTransferCurrencyRequest = in
	if f.transferCurrencyErr != nil {
		return nil, f.transferCurrencyErr
	}
	return f.transferCurrencyResp, nil
}

func (f *fakeSystemEngineClient) GetItemInfo(_ context.Context, in *systemenginepb.GetItemInfoRequest, _ ...grpc.CallOption) (*systemenginepb.GetItemInfoResponse, error) {
	f.lastGetItemInfoRequest = in
	if f.getItemInfoErr != nil {
		return nil, f.getItemInfoErr
	}
	return f.getItemInfoResp, nil
}

func (f *fakeSystemEngineClient) ListInventory(_ context.Context, in *systemenginepb.ListInventoryRequest, _ ...grpc.CallOption) (*systemenginepb.ListInventoryResponse, error) {
	f.lastListInventoryRequest = in
	if f.listInventoryErr != nil {
		return nil, f.listInventoryErr
	}
	return f.listInventoryResp, nil
}

func (f *fakeSystemEngineClient) GetAvailableActions(_ context.Context, in *systemenginepb.GetAvailableActionsRequest, _ ...grpc.CallOption) (*systemenginepb.GetAvailableActionsResponse, error) {
	f.lastGetAvailableActionsRequest = in
	if f.getAvailableActionsFunc != nil {
		return f.getAvailableActionsFunc(in)
	}
	if f.getAvailableActionsErr != nil {
		return nil, f.getAvailableActionsErr
	}
	return f.getAvailableActionsResp, nil
}

func (f *fakeSystemEngineClient) GetCharacterSchema(context.Context, *systemenginepb.GetCharacterSchemaRequest, ...grpc.CallOption) (*systemenginepb.GetCharacterSchemaResponse, error) {
	if f.getCharacterSchemaErr != nil {
		return nil, f.getCharacterSchemaErr
	}
	return f.getCharacterSchemaResp, nil
}

func (f *fakeSystemEngineClient) GetCharacterStatus(_ context.Context, in *systemenginepb.GetCharacterStatusRequest, _ ...grpc.CallOption) (*systemenginepb.GetCharacterStatusResponse, error) {
	f.lastGetCharacterStatusRequest = in
	if f.getCharacterStatusFunc != nil {
		return f.getCharacterStatusFunc(in)
	}
	if f.getCharacterStatusErr != nil {
		return nil, f.getCharacterStatusErr
	}
	return f.getCharacterStatusResp, nil
}

func (f *fakeSystemEngineClient) StartTurn(_ context.Context, in *systemenginepb.StartTurnRequest, _ ...grpc.CallOption) (*systemenginepb.StartTurnResponse, error) {
	f.startTurnRequests = append(f.startTurnRequests, in)
	if f.startTurnFunc != nil {
		return f.startTurnFunc(in)
	}
	if f.startTurnErr != nil {
		return nil, f.startTurnErr
	}
	return f.startTurnResp, nil
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
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, fakeEngine, st, nil, nil, st).Handler())
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
