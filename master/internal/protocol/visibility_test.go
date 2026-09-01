// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import "testing"

func TestVisibilityScopeKind_IsValid(t *testing.T) {
	tests := []struct {
		name string
		v    VisibilityScopeKind
		want bool
	}{
		{name: "Unspecified_ReturnsFalse", v: VisibilityScopeUnspecified, want: false},
		{name: "Public_ReturnsTrue", v: VisibilityScopePublic, want: true},
		{name: "Private_ReturnsTrue", v: VisibilityScopePrivate, want: true},
		{name: "UnrecognizedScope_ReturnsFalse", v: VisibilityScopeKind("secret"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.IsValid(); got != tt.want {
				t.Errorf("VisibilityScopeKind(%q).IsValid() = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
