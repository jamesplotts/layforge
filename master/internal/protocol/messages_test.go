// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

// TestMessage_MarshalJSON_FlattensEnvelopeAndPayload guards against the
// embedding regressing into a nested "Envelope" key — protocol/
// asyncapi.yaml's allOf composition requires envelope fields and
// "payload" to sit side by side in one flat object.
func TestMessage_MarshalJSON_FlattensEnvelopeAndPayload(t *testing.T) {
	msg := SystemConnectMessage{
		Envelope: Envelope{
			ProtocolVersion: CurrentProtocolVersion,
			MessageID:       "msg-1",
			Timestamp:       time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			SenderID:        "client-1",
			CampaignID:      "campaign-1",
			Type:            MessageTypeSystemConnect,
		},
		Payload: SystemConnectPayload{
			ClientKind: "player_web_v1",
			AuthToken:  "token-1",
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	for _, field := range []string{"protocol_version", "message_id", "timestamp", "sender_id", "campaign_id", "type", "payload"} {
		if _, ok := got[field]; !ok {
			t.Errorf("marshaled JSON missing top-level field %q; got %v", field, got)
		}
	}
	if _, ok := got["Envelope"]; ok {
		t.Errorf("marshaled JSON has a nested \"Envelope\" key, embedding did not flatten: %v", got)
	}

	payload, ok := got["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload field is not an object: %v", got["payload"])
	}
	if payload["client_kind"] != "player_web_v1" {
		t.Errorf("payload.client_kind = %v, want %q", payload["client_kind"], "player_web_v1")
	}
}

func TestMessage_UnmarshalJSON_RoundTrips(t *testing.T) {
	const wire = `{
		"protocol_version": "0.1.0",
		"message_id": "msg-1",
		"timestamp": "2026-09-01T12:00:00Z",
		"sender_id": "client-1",
		"campaign_id": "campaign-1",
		"type": "system.connect",
		"payload": {"client_kind": "player_web_v1", "auth_token": "token-1"}
	}`

	var msg SystemConnectMessage
	if err := json.Unmarshal([]byte(wire), &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if msg.Type != MessageTypeSystemConnect {
		t.Errorf("Type = %q, want %q", msg.Type, MessageTypeSystemConnect)
	}
	if msg.Payload.ClientKind != "player_web_v1" {
		t.Errorf("Payload.ClientKind = %q, want %q", msg.Payload.ClientKind, "player_web_v1")
	}
	if err := msg.Envelope.Validate(); err != nil {
		t.Errorf("Envelope.Validate() = %v, want nil", err)
	}
}
