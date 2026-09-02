// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import "testing"

func TestLooksLikeMalformedToolCall(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "RealExample_GarbledOpeningTag_ReturnsTrue",
			// Observed live against a real Ollama server (qwen2.5:32b) —
			// see runSlowPass's call site.
			text: "rPid\n{\n\"name\": \"resolve_check\",\n\"arguments\": {\n\"character_id\": \"abc\"\n}\n}\n</tool_call>",
			want: true,
		},
		{
			name: "WellFormedToolCallTag_ReturnsTrue",
			text: "<tool_call>\n{\"name\": \"advance_turn\", \"arguments\": {}}\n</tool_call>",
			want: true,
		},
		{
			name: "BareJSONWithNameAndArguments_ReturnsTrue",
			text: `{"name": "end_combat", "arguments": {}}`,
			want: true,
		},
		{
			name: "NormalNarration_ReturnsFalse",
			text: "The goblin springs from the underbrush, brandishing a crude spear.",
			want: false,
		},
		{
			name: "NarrationMentioningACharacterNamedArgument_ReturnsFalse",
			text: "Kestrel's name echoes through the hall as the crowd argues over the outcome.",
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
			if got := looksLikeMalformedToolCall(tt.text); got != tt.want {
				t.Errorf("looksLikeMalformedToolCall(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
