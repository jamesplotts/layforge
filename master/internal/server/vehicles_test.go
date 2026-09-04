// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
)

func TestServe_NarrativePlayerInput_SlowPass_ListVehicles_Success_EvenWithNoneCreatedYet(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("list_vehicles", `{}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-list-vehicles", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-list-vehicles", "player-a", "char-a", "What do we have to travel with?"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if !toolResult.Payload.Success {
		t.Fatalf("tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_ListVehicles_NotConfigured_ReturnsFailure(t *testing.T) {
	fakeLLM := toolCallLLM("list_vehicles", `{}`)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// No vehicles store at all — a deployment that never configured one.
	ts := httptest.NewServer(server.New(logger, nil, fakeLLM, "test-model", nil, &fakeSystemEngineClient{}, nil, nil, nil, nil, nil, nil).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-vehicles-unconfigured", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-vehicles-unconfigured", "player-a", "char-a", "What do we have to travel with?"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "vehicles_not_configured" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "vehicles_not_configured")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AcquireVehicle_NoPartyLocation_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("acquire_vehicle", `{"name":"Old Nag","vehicle_type":"mount"}`)

	ts, _ := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-acquire-nolocation", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-acquire-nolocation", "player-a", "char-a", "I buy a horse."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "no_party_location" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "no_party_location")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_AcquireVehicle_Success_StartsTravelingWithParty(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("acquire_vehicle", `{"name":"Old Nag","vehicle_type":"mount"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-acquire")
	if err := st.SetPartyLocation(context.Background(), "campaign-acquire", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-acquire", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-acquire", "player-a", "char-a", "I buy a horse."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	// dmAcquireVehicle's own real vehicle.imported broadcast can land
	// before or after tool.result on this same connection — skip past it
	// rather than assuming a strict order.
	var toolResult protocol.ToolResultMessage
	for {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading next message: %v", err)
		}
		if typ != protocol.MessageTypeToolResult {
			continue
		}
		if err := json.Unmarshal(data, &toolResult); err != nil {
			t.Fatalf("unmarshaling tool.result: %v", err)
		}
		break
	}
	if !toolResult.Payload.Success {
		t.Fatalf("tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}

	vehicles, err := st.ListVehicles(ctx, "campaign-acquire")
	if err != nil {
		t.Fatalf("ListVehicles() error = %v", err)
	}
	if len(vehicles) != 1 || vehicles[0].Name != "Old Nag" || vehicles[0].Stabled {
		t.Fatalf("ListVehicles() = %+v, want one Old Nag, Stabled=false", vehicles)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_StableThenTakeVehicle_RoundTrips(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("stable_vehicle", `{"vehicle_id":"vehicle-1"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-stable-vehicle")
	ctx0 := context.Background()
	if err := st.SetPartyLocation(ctx0, "campaign-stable-vehicle", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}
	if err := st.CreateVehicle(ctx0, "vehicle-1", "campaign-stable-vehicle", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-stable-vehicle", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-stable-vehicle", "player-a", "char-a", "I stable the horse here."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if !toolResult.Payload.Success {
		t.Fatalf("stable tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}

	v, _, err := st.GetVehicle(ctx, "campaign-stable-vehicle", "vehicle-1")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if !v.Stabled || v.LocationID != "keep-stonewatch" {
		t.Fatalf("GetVehicle() = %+v, want Stabled=true LocationID=keep-stonewatch", v)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_StableVehicle_AlreadyStabled_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("stable_vehicle", `{"vehicle_id":"vehicle-1"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-stable-twice")
	ctx0 := context.Background()
	if err := st.SetPartyLocation(ctx0, "campaign-stable-twice", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}
	if err := st.CreateVehicle(ctx0, "vehicle-1", "campaign-stable-twice", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}
	if _, err := st.StableVehicle(ctx0, "campaign-stable-twice", "vehicle-1", "old-road"); err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-stable-twice", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-stable-twice", "player-a", "char-a", "I stable the horse here too."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "already_stabled" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "already_stabled")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TakeVehicle_Success_ClearsStabled(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("take_vehicle", `{"vehicle_id":"vehicle-1"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-take-vehicle")
	ctx0 := context.Background()
	if err := st.CreateVehicle(ctx0, "vehicle-1", "campaign-take-vehicle", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}
	if _, err := st.StableVehicle(ctx0, "campaign-take-vehicle", "vehicle-1", "keep-stonewatch"); err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}
	if err := st.SetPartyLocation(ctx0, "campaign-take-vehicle", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-take-vehicle", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-take-vehicle", "player-a", "char-a", "I take the horse from the stable."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if !toolResult.Payload.Success {
		t.Fatalf("take tool.result Success = false, want true (payload: %+v)", toolResult.Payload)
	}

	v, _, err := st.GetVehicle(ctx, "campaign-take-vehicle", "vehicle-1")
	if err != nil {
		t.Fatalf("GetVehicle() error = %v", err)
	}
	if v.Stabled {
		t.Fatalf("GetVehicle() = %+v, want Stabled=false", v)
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TakeVehicle_WrongLocation_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("take_vehicle", `{"vehicle_id":"vehicle-1"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-take-wrong-location")
	ctx0 := context.Background()
	if err := st.CreateVehicle(ctx0, "vehicle-1", "campaign-take-wrong-location", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}
	// Stabled at keep-stonewatch, but the party is at old-road.
	if _, err := st.StableVehicle(ctx0, "campaign-take-wrong-location", "vehicle-1", "keep-stonewatch"); err != nil {
		t.Fatalf("StableVehicle() error = %v", err)
	}
	if err := st.SetPartyLocation(ctx0, "campaign-take-wrong-location", "old-road"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-take-wrong-location", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-take-wrong-location", "player-a", "char-a", "I take the horse."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "wrong_location" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "wrong_location")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_TakeVehicle_NotStabled_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{}
	fakeLLM := toolCallLLM("take_vehicle", `{"vehicle_id":"vehicle-1"}`)

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	bindPack(t, st, "campaign-take-not-stabled")
	ctx0 := context.Background()
	if err := st.CreateVehicle(ctx0, "vehicle-1", "campaign-take-not-stabled", "Old Nag", "mount"); err != nil {
		t.Fatalf("CreateVehicle() error = %v", err)
	}
	if err := st.SetPartyLocation(ctx0, "campaign-take-not-stabled", "keep-stonewatch"); err != nil {
		t.Fatalf("SetPartyLocation() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-take-not-stabled", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-take-not-stabled", "player-a", "char-a", "I take the horse."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false")
	}
	if toolResult.Payload.ReasonCode != "not_stabled" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "not_stabled")
	}
}

func vehicleImportMessage(campaignID, sender, name, vehicleType string) protocol.VehicleImportMessage {
	return protocol.VehicleImportMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "import-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeVehicleImport,
		},
		Payload: protocol.VehicleImportPayload{Name: name, VehicleType: vehicleType},
	}
}

func TestServe_VehicleImport_Success_SavesAndBroadcastsImported(t *testing.T) {
	ts, st := newTestServerWithLLMAndSystemEngine(t, nil, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-vehicle-import", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := wsjson.Write(ctx, conn, vehicleImportMessage("campaign-vehicle-import", "player-a", "Old Nag", "mount")); err != nil {
		t.Fatalf("Write(vehicle.import) error = %v", err)
	}

	var imported protocol.VehicleImportedMessage
	if err := wsjson.Read(ctx, conn, &imported); err != nil {
		t.Fatalf("Read(vehicle.imported) error = %v", err)
	}
	if imported.Payload.Name != "Old Nag" || imported.Payload.VehicleType != "mount" {
		t.Errorf("vehicle.imported payload = %+v, want Name=Old Nag VehicleType=mount", imported.Payload)
	}
	if imported.Payload.VehicleID == "" {
		t.Error("vehicle.imported VehicleID is empty, want a generated id")
	}

	vehicles, err := st.ListVehicles(ctx, "campaign-vehicle-import")
	if err != nil {
		t.Fatalf("ListVehicles() error = %v", err)
	}
	if len(vehicles) != 1 || vehicles[0].Name != "Old Nag" || vehicles[0].Stabled {
		t.Fatalf("ListVehicles() = %+v, want one Old Nag, Stabled=false", vehicles)
	}
}

func TestServe_VehicleImport_MissingFields_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerWithLLMAndSystemEngine(t, nil, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-vehicle-import-bad", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := wsjson.Write(ctx, conn, vehicleImportMessage("campaign-vehicle-import-bad", "player-a", "", "mount")); err != nil {
		t.Fatalf("Write(vehicle.import) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_VehicleImport_NotConfigured_ReturnsSystemError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-vehicle-import-unconfigured", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := wsjson.Write(ctx, conn, vehicleImportMessage("campaign-vehicle-import-unconfigured", "player-a", "Old Nag", "mount")); err != nil {
		t.Fatalf("Write(vehicle.import) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}
