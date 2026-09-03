// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import (
	"errors"
	"testing"
	"time"
)

func validEnvelope() Envelope {
	return Envelope{
		ProtocolVersion: CurrentProtocolVersion,
		MessageID:       "msg-1",
		Timestamp:       time.Now().UTC(),
		SenderID:        "sender-1",
		CampaignID:      "campaign-1",
		Type:            MessageTypeSystemConnect,
	}
}

func TestEnvelope_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(e Envelope) Envelope
		wantErr error // nil means "expect success"
	}{
		{
			name:    "AllFieldsPresent_ReturnsNil",
			modify:  func(e Envelope) Envelope { return e },
			wantErr: nil,
		},
		{
			name:    "WrongProtocolVersion_ReturnsUnsupportedProtocolVersion",
			modify:  func(e Envelope) Envelope { e.ProtocolVersion = "0.0.1"; return e },
			wantErr: ErrUnsupportedProtocolVersion,
		},
		{
			name:    "MissingMessageID_ReturnsMissingMessageID",
			modify:  func(e Envelope) Envelope { e.MessageID = ""; return e },
			wantErr: ErrMissingMessageID,
		},
		{
			name:    "ZeroTimestamp_ReturnsMissingTimestamp",
			modify:  func(e Envelope) Envelope { e.Timestamp = time.Time{}; return e },
			wantErr: ErrMissingTimestamp,
		},
		{
			name:    "MissingSenderID_ReturnsMissingSenderID",
			modify:  func(e Envelope) Envelope { e.SenderID = ""; return e },
			wantErr: ErrMissingSenderID,
		},
		{
			name:    "MissingCampaignID_ReturnsMissingCampaignID",
			modify:  func(e Envelope) Envelope { e.CampaignID = ""; return e },
			wantErr: ErrMissingCampaignID,
		},
		{
			name:    "UnrecognizedType_ReturnsUnrecognizedType",
			modify:  func(e Envelope) Envelope { e.Type = MessageType("map.room_adjacency"); return e },
			wantErr: ErrUnrecognizedType,
		},
		{
			name:    "UnspecifiedType_ReturnsUnrecognizedType",
			modify:  func(e Envelope) Envelope { e.Type = MessageTypeUnspecified; return e },
			wantErr: ErrUnrecognizedType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.modify(validEnvelope()).Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want error wrapping %v", err, tt.wantErr)
			}
		})
	}
}
