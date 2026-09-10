// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

// clientEnvelope builds a filled Envelope for a client.* message test.
func clientEnvelope(t MessageType) Envelope {
	return Envelope{
		ProtocolVersion: CurrentProtocolVersion,
		MessageID:       "msg-1",
		Timestamp:       time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		SenderID:        "master",
		CampaignID:      "campaign-1",
		Type:            t,
	}
}

// TestClientMessages_RoundTrip marshals then unmarshals one message of
// every client.* type and checks a distinctive field survives — a guard
// that the payload tags and the Message[T] flattening are consistent
// across the whole family, including the roll.* messages nothing emits
// yet.
func TestClientMessages_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		msg   any
		check func(t *testing.T, wire []byte)
	}{
		{
			name: "client.display",
			msg: ClientDisplayMessage{
				Envelope: clientEnvelope(MessageTypeClientDisplay),
				Payload:  ClientDisplayPayload{Recipient: "discord:1", Text: "Reorx hits the bugbear."},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientDisplayMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Recipient != "discord:1" || got.Payload.Text != "Reorx hits the bugbear." {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.query",
			msg: ClientQueryMessage{
				Envelope: clientEnvelope(MessageTypeClientQuery),
				Payload: ClientQueryPayload{
					PromptID: "p-1", PromptText: "What's your character's name?",
					SubmitLabel: "Continue", AcceptsFileUpload: false,
				},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientQueryMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.PromptID != "p-1" || got.Payload.SubmitLabel != "Continue" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.query_response",
			msg: ClientQueryResponseMessage{
				Envelope: clientEnvelope(MessageTypeClientQueryResponse),
				Payload:  ClientQueryResponsePayload{PromptID: "p-1", Text: "Reorx"},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientQueryResponseMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Text != "Reorx" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.choice",
			msg: ClientChoiceMessage{
				Envelope: clientEnvelope(MessageTypeClientChoice),
				Payload: ClientChoicePayload{
					PromptID: "p-2", PromptText: "Choose your race.",
					Options: []ClientChoiceOption{{Value: "Human", Label: "Human"}, {Value: "Dwarf", Label: "Dwarf"}},
				},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientChoiceMessage
				mustUnmarshal(t, wire, &got)
				if len(got.Payload.Options) != 2 || got.Payload.Options[1].Value != "Dwarf" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.choice_response",
			msg: ClientChoiceResponseMessage{
				Envelope: clientEnvelope(MessageTypeClientChoiceResponse),
				Payload:  ClientChoiceResponsePayload{PromptID: "p-2", Value: "Dwarf"},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientChoiceResponseMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Value != "Dwarf" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.image",
			msg: ClientImageMessage{
				Envelope: clientEnvelope(MessageTypeClientImage),
				Payload:  ClientImagePayload{ImageURL: "http://host/view/1.png", Caption: "The sunken vault"},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientImageMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.ImageURL != "http://host/view/1.png" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.roll",
			msg: ClientRollMessage{
				Envelope: clientEnvelope(MessageTypeClientRoll),
				Payload: ClientRollPayload{
					PromptID: "r-1", CharacterID: "c-1", Purpose: ClientRollPurposeAttack,
					Text: "Roll to attack with Reorx's battle axe",
					Dice: []ClientRollDie{{ID: "d1", Sides: 20, Result: 14}},
				},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientRollMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Purpose != ClientRollPurposeAttack || got.Payload.Dice[0].Result != 14 {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.roll_spectate omits result",
			msg: ClientRollSpectateMessage{
				Envelope: clientEnvelope(MessageTypeClientRollSpectate),
				Payload: ClientRollSpectatePayload{
					PromptID: "r-1", CharacterID: "c-1", Purpose: ClientRollPurposeAttack,
					Text: "Reorx is rolling their attack",
					Dice: []ClientRollDie{{ID: "d1", Sides: 20}},
				},
			},
			check: func(t *testing.T, wire []byte) {
				var raw map[string]any
				mustUnmarshal(t, wire, &raw)
				dice := raw["payload"].(map[string]any)["dice"].([]any)
				if _, present := dice[0].(map[string]any)["result"]; present {
					t.Errorf("client.roll_spectate leaked a die result to spectators: %v", dice[0])
				}
			},
		},
		{
			name: "client.roll_reveal",
			msg: ClientRollRevealMessage{
				Envelope: clientEnvelope(MessageTypeClientRollReveal),
				Payload:  ClientRollRevealPayload{PromptID: "r-1", DieID: "d1"},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientRollRevealMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.DieID != "d1" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.roll_spectate_reveal",
			msg: ClientRollSpectateRevealMessage{
				Envelope: clientEnvelope(MessageTypeClientRollSpectateReveal),
				Payload:  ClientRollSpectateRevealPayload{PromptID: "r-1", DieID: "d1", Result: 14},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientRollSpectateRevealMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Result != 14 {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
		{
			name: "client.roll_complete",
			msg: ClientRollCompleteMessage{
				Envelope: clientEnvelope(MessageTypeClientRollComplete),
				Payload:  ClientRollCompletePayload{PromptID: "r-1", Total: 20, ResultSummary: "Hit"},
			},
			check: func(t *testing.T, wire []byte) {
				var got ClientRollCompleteMessage
				mustUnmarshal(t, wire, &got)
				if got.Payload.Total != 20 || got.Payload.ResultSummary != "Hit" {
					t.Errorf("got %+v", got.Payload)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			// Every client.* message is a recognized type.
			var env Envelope
			mustUnmarshal(t, wire, &env)
			if !env.Type.IsValid() {
				t.Errorf("Type %q is not IsValid()", env.Type)
			}
			if err := env.Validate(); err != nil {
				t.Errorf("Envelope.Validate() = %v", err)
			}
			tt.check(t, wire)
		})
	}
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}

// TestClientRollPurpose_IsValid mirrors the other enum-sentinel tests.
func TestClientRollPurpose_IsValid(t *testing.T) {
	for _, p := range []ClientRollPurpose{
		ClientRollPurposeAttack, ClientRollPurposeDamage, ClientRollPurposeSave,
		ClientRollPurposeCheck, ClientRollPurposeInitiative, ClientRollPurposeDeathSave,
		ClientRollPurposeCustom,
	} {
		if !p.IsValid() {
			t.Errorf("%q.IsValid() = false, want true", p)
		}
	}
	for _, p := range []ClientRollPurpose{ClientRollPurposeUnspecified, "bogus"} {
		if p.IsValid() {
			t.Errorf("%q.IsValid() = true, want false", p)
		}
	}
}
