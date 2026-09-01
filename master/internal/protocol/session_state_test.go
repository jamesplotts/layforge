// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import "testing"

func TestSessionState_IsValid(t *testing.T) {
	tests := []struct {
		name string
		s    SessionState
		want bool
	}{
		{name: "Unspecified_ReturnsFalse", s: SessionStateUnspecified, want: false},
		{name: "Joined_ReturnsTrue", s: SessionStateJoined, want: true},
		{name: "Left_ReturnsTrue", s: SessionStateLeft, want: true},
		{name: "Paused_ReturnsTrue", s: SessionStatePaused, want: true},
		{name: "Resumed_ReturnsTrue", s: SessionStateResumed, want: true},
		{name: "UnrecognizedState_ReturnsFalse", s: SessionState("banished"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("SessionState(%q).IsValid() = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
