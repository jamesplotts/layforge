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
		{name: "UnrecognizedType_ReturnsFalse", t: MessageType("narrative.dm_prose"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.IsValid(); got != tt.want {
				t.Errorf("MessageType(%q).IsValid() = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
}
