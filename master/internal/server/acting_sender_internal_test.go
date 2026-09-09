// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"testing"

	"github.com/jamesplotts/layforge/master/internal/auth"
)

func TestActingSender_UsesAccountWhenAuthenticated(t *testing.T) {
	tests := []struct {
		name     string
		cs       *connState
		envelope string
		want     string
	}{
		{
			name:     "unauthenticated falls through to envelope sender_id",
			cs:       &connState{},
			envelope: "Kestrel",
			want:     "Kestrel",
		},
		{
			name:     "nil connState falls through",
			cs:       nil,
			envelope: "Kestrel",
			want:     "Kestrel",
		},
		{
			name:     "authenticated connection acts as its account, ignoring a forged sender_id",
			cs:       &connState{identity: auth.Identity{AccountID: "discord:99", DisplayName: "Real Player"}},
			envelope: "someone-elses-character",
			want:     "discord:99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actingSender(tt.cs, tt.envelope); got != tt.want {
				t.Errorf("actingSender() = %q, want %q", got, tt.want)
			}
		})
	}
}
