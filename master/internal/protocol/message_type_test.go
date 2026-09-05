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
		{name: "CharacterSchemaRequest_ReturnsTrue", t: MessageTypeCharacterSchemaRequest, want: true},
		{name: "CharacterSchemaResponse_ReturnsTrue", t: MessageTypeCharacterSchemaResponse, want: true},
		{name: "CharacterGet_ReturnsTrue", t: MessageTypeCharacterGet, want: true},
		{name: "CharacterState_ReturnsTrue", t: MessageTypeCharacterState, want: true},
		{name: "CharacterApplyEffect_ReturnsTrue", t: MessageTypeCharacterApplyEffect, want: true},
		{name: "NarrativeDmProse_ReturnsTrue", t: MessageTypeNarrativeDmProse, want: true},
		{name: "ToolResult_ReturnsTrue", t: MessageTypeToolResult, want: true},
		{name: "TurnState_ReturnsTrue", t: MessageTypeTurnState, want: true},
		{name: "NarrativeSceneImage_ReturnsTrue", t: MessageTypeNarrativeSceneImage, want: true},
		{name: "MapTokenState_ReturnsTrue", t: MessageTypeMapTokenState, want: true},
		{name: "MapTokenMoveRequest_ReturnsTrue", t: MessageTypeMapTokenMoveRequest, want: true},
		{name: "VehicleImport_ReturnsTrue", t: MessageTypeVehicleImport, want: true},
		{name: "VehicleImported_ReturnsTrue", t: MessageTypeVehicleImported, want: true},
		{name: "AudioChunk_ReturnsTrue", t: MessageTypeAudioChunk, want: true},
		{name: "AudioTranscription_ReturnsTrue", t: MessageTypeAudioTranscription, want: true},
		{name: "CharacterCreationStart_ReturnsTrue", t: MessageTypeCharacterCreationStart, want: true},
		{name: "CharacterCreationPrompt_ReturnsTrue", t: MessageTypeCharacterCreationPrompt, want: true},
		{name: "CharacterCreationAnswer_ReturnsTrue", t: MessageTypeCharacterCreationAnswer, want: true},
		{name: "CharacterReviewResult_ReturnsTrue", t: MessageTypeCharacterReviewResult, want: true},
		{name: "UnrecognizedType_ReturnsFalse", t: MessageType("map.room_adjacency"), want: false},
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
