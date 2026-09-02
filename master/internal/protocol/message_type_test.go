// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import "testing"

func TestMessageType_IsValid(t *testing.T) {
	tests := []struct {
		name string
		t    MessageType
		want bool
	}{
		{name: "Unspecified_ReturnsFalse", t: MessageTypeUnspecified, want: false},
		{name: "SystemConnect_ReturnsTrue", t: MessageTypeSystemConnect, want: true},
		{name: "SystemSessionState_ReturnsTrue", t: MessageTypeSystemSessionState, want: true},
		{name: "SystemError_ReturnsTrue", t: MessageTypeSystemError, want: true},
		{name: "SafetyFlag_ReturnsTrue", t: MessageTypeSafetyFlag, want: true},
		{name: "SafetyFlagBroadcast_ReturnsTrue", t: MessageTypeSafetyFlagBroadcast, want: true},
		{name: "LogHistoryRequest_ReturnsTrue", t: MessageTypeLogHistoryRequest, want: true},
		{name: "LogHistoryResponse_ReturnsTrue", t: MessageTypeLogHistoryResponse, want: true},
		{name: "NarrativePlayerInput_ReturnsTrue", t: MessageTypeNarrativePlayerInput, want: true},
		{name: "NarrativePlayerBubble_ReturnsTrue", t: MessageTypeNarrativePlayerBubble, want: true},
		{name: "CharacterUpload_ReturnsTrue", t: MessageTypeCharacterUpload, want: true},
		{name: "CharacterValidationResult_ReturnsTrue", t: MessageTypeCharacterValidationResult, want: true},
		{name: "RollCheckRequest_ReturnsTrue", t: MessageTypeRollCheckRequest, want: true},
		{name: "RollRequest_ReturnsTrue", t: MessageTypeRollRequest, want: true},
		{name: "RollResult_ReturnsTrue", t: MessageTypeRollResult, want: true},
		{name: "UnrecognizedType_ReturnsFalse", t: MessageType("narrative.dm_prose"), want: false},
		{name: "CharacterReviewStatus_ReturnsFalse_NotImplementedYet", t: MessageType("character.review_status"), want: false},
		{name: "RollAcknowledge_ReturnsFalse_NotImplementedYet", t: MessageType("roll.acknowledge"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.IsValid(); got != tt.want {
				t.Errorf("MessageType(%q).IsValid() = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
}
