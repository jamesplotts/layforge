// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"testing"

	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func TestCharacterStatusString(t *testing.T) {
	tests := []struct {
		name      string
		status    systemenginepb.CharacterStatus
		wantValue string
		wantOK    bool
	}{
		{"Active_ReturnsActive", systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE, "active", true},
		{"Unconscious_ReturnsUnconscious", systemenginepb.CharacterStatus_CHARACTER_STATUS_UNCONSCIOUS, "unconscious", true},
		{"Dying_ReturnsDying", systemenginepb.CharacterStatus_CHARACTER_STATUS_DYING, "dying", true},
		{"Dead_ReturnsDead", systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD, "dead", true},
		{"Unspecified_ReturnsNotOK", systemenginepb.CharacterStatus_CHARACTER_STATUS_UNSPECIFIED, "", false},
		{"OutOfRangeValue_ReturnsNotOK", systemenginepb.CharacterStatus(99), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, ok := characterStatusString(tt.status)
			if value != tt.wantValue || ok != tt.wantOK {
				t.Errorf("characterStatusString(%v) = (%q, %v), want (%q, %v)", tt.status, value, ok, tt.wantValue, tt.wantOK)
			}
		})
	}
}
