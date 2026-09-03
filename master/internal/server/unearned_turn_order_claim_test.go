// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import "testing"

func TestLooksLikeUnearnedTurnOrderClaim(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "RealExample_InitiativeRolledAndStrikesFirst_ReturnsTrue",
			// Observed live against a real Ollama server (qwen2.5:32b):
			// narrated after start_combat had already failed — see
			// runSlowPass's turnOrderCallFailed doc comment.
			text: "The initiative is rolled and it seems the Goblin Scouts strike first.",
			want: true,
		},
		{
			name: "TurnOrderPhrase_ReturnsTrue",
			text: "Turn order is set, and you'll act first this round.",
			want: true,
		},
		{
			name: "WhoseTurnPhrase_ReturnsTrue",
			text: "It's unclear whose turn it is until the dust settles.",
			want: true,
		},
		{
			name: "GoesFirstPhrase_ReturnsTrue",
			text: "Quicker on their feet, the goblin goes first.",
			want: true,
		},
		{
			name: "NormalFightNarration_ReturnsFalse",
			text: "The goblin springs from the underbrush, spear leveled, and the fight is on.",
			want: false,
		},
		{
			name: "Empty_ReturnsFalse",
			text: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeUnearnedTurnOrderClaim(tt.text); got != tt.want {
				t.Errorf("looksLikeUnearnedTurnOrderClaim(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
