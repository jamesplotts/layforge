// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func requestCharacterSchema(ctx context.Context, conn *websocket.Conn, sender, campaignID string) error {
	msg := protocol.CharacterSchemaRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "schema-req-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterSchemaRequest,
		},
		Payload: protocol.CharacterSchemaRequestPayload{},
	}
	return wsjson.Write(ctx, conn, msg)
}

func requestCharacterGet(ctx context.Context, conn *websocket.Conn, sender, campaignID, characterID string) error {
	msg := protocol.CharacterGetMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "char-get-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterGet,
		},
		Payload: protocol.CharacterGetPayload{CharacterID: characterID},
	}
	return wsjson.Write(ctx, conn, msg)
}

func TestServe_CharacterSchemaRequest_ReturnsSchema(t *testing.T) {
	fake := &fakeSystemEngineClient{
		getCharacterSchemaResp: &systemenginepb.GetCharacterSchemaResponse{
			SchemaVersion: "opencombatengine-v1",
			JsonSchema:    `{"type":"object"}`,
		},
	}
	ts, _ := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-schema", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterSchema(ctx, conn, "player-a", "campaign-schema"); err != nil {
		t.Fatalf("requestCharacterSchema() error = %v", err)
	}

	var resp protocol.CharacterSchemaResponseMessage
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read(character.schema_response) error = %v", err)
	}
	if resp.Payload.SchemaVersion != "opencombatengine-v1" {
		t.Errorf("SchemaVersion = %q, want %q", resp.Payload.SchemaVersion, "opencombatengine-v1")
	}
	if resp.Payload.JSONSchema != `{"type":"object"}` {
		t.Errorf("JSONSchema = %q, want %q", resp.Payload.JSONSchema, `{"type":"object"}`)
	}
}

func TestServe_CharacterSchemaRequest_NoSystemEngineConfigured_RespondsWithSystemError(t *testing.T) {
	ts := newTestServer(t) // no system engine configured
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-1", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterSchema(ctx, conn, "player-a", "campaign-1"); err != nil {
		t.Fatalf("requestCharacterSchema() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_CharacterGet_OwnedCharacter_ReturnsStateWithStatus(t *testing.T) {
	fake := &fakeSystemEngineClient{
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{
			Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE,
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-get", "player-a")

	conn := dialAndJoin(t, ts, "campaign-get", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterGet(ctx, conn, "player-a", "campaign-get", "char-1"); err != nil {
		t.Fatalf("requestCharacterGet() error = %v", err)
	}

	var resp protocol.CharacterStateMessage
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read(character.state) error = %v", err)
	}
	if resp.Payload.CharacterID != "char-1" {
		t.Errorf("CharacterID = %q, want %q", resp.Payload.CharacterID, "char-1")
	}
	if resp.Payload.Status != "active" {
		t.Errorf("Status = %q, want %q", resp.Payload.Status, "active")
	}
	if resp.Payload.SchemaVersion != "opencombatengine-v1" {
		t.Errorf("SchemaVersion = %q, want %q", resp.Payload.SchemaVersion, "opencombatengine-v1")
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Payload.CharacterData, &data); err != nil {
		t.Fatalf("unmarshaling CharacterData error = %v", err)
	}
	if data["name"] != "Kestrel" {
		t.Errorf("CharacterData[name] = %v, want %q", data["name"], "Kestrel")
	}

	if fake.lastGetCharacterStatusRequest.Actor.ActorId != "char-1" {
		t.Errorf("GetCharacterStatus called with Actor.ActorId = %q, want %q", fake.lastGetCharacterStatusRequest.Actor.ActorId, "char-1")
	}
}

func TestServe_CharacterGet_CharacterOwnedBySomeoneElse_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-get", "player-a")

	conn := dialAndJoin(t, ts, "campaign-get", "player-b")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterGet(ctx, conn, "player-b", "campaign-get", "char-1"); err != nil {
		t.Fatalf("requestCharacterGet() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (player-b does not own char-1)", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_CharacterGet_UnknownCharacter_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{}
	ts, _ := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-get", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterGet(ctx, conn, "player-a", "campaign-get", "does-not-exist"); err != nil {
		t.Fatalf("requestCharacterGet() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q", typ, protocol.MessageTypeSystemError)
	}
}

func TestServe_CharacterGet_EngineReturnsUnspecifiedStatus_RespondsWithSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{
			Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_UNSPECIFIED,
		},
	}
	ts, st := newTestServerWithSystemEngine(t, fake)
	defer ts.Close()
	seedCharacter(t, st, "char-1", "campaign-get", "player-a")

	conn := dialAndJoin(t, ts, "campaign-get", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := requestCharacterGet(ctx, conn, "player-a", "campaign-get", "char-1"); err != nil {
		t.Fatalf("requestCharacterGet() error = %v", err)
	}

	typ, _, err := readEnvelopeType(ctx, conn)
	if err != nil {
		t.Fatalf("reading response error = %v", err)
	}
	if typ != protocol.MessageTypeSystemError {
		t.Fatalf("response type = %q, want %q (an Unspecified status should not be forwarded to the client)", typ, protocol.MessageTypeSystemError)
	}
}
