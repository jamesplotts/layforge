// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import "testing"

func TestNewMessageID_TwoCalls_ReturnDistinctNonEmptyValues(t *testing.T) {
	first, err := newMessageID()
	if err != nil {
		t.Fatalf("newMessageID() error = %v", err)
	}
	if first == "" {
		t.Fatal("newMessageID() returned empty string")
	}

	second, err := newMessageID()
	if err != nil {
		t.Fatalf("newMessageID() error = %v", err)
	}
	if first == second {
		t.Errorf("newMessageID() returned the same value twice: %q", first)
	}
}

func TestNewCharacterID_TwoCalls_ReturnDistinctNonEmptyValues(t *testing.T) {
	first, err := newCharacterID()
	if err != nil {
		t.Fatalf("newCharacterID() error = %v", err)
	}
	if first == "" {
		t.Fatal("newCharacterID() returned empty string")
	}

	second, err := newCharacterID()
	if err != nil {
		t.Fatalf("newCharacterID() error = %v", err)
	}
	if first == second {
		t.Errorf("newCharacterID() returned the same value twice: %q", first)
	}
}
