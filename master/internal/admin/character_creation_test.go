// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin_test

import (
	"net/http"
	"testing"
)

// These cover the admin handler's own guard logic. The RPC relay itself
// (map request -> StartCharacterCreation/AnswerCharacterCreationPrompt ->
// map response) is a thin pass-through; the engine contract it relays is
// exercised against a fake in internal/server/character_creation_test.go
// and for real in OpenCombatEngine's own suite.

func TestCharacterCreation_NoSystemEngine_ReturnsError(t *testing.T) {
	_, httpSrv := newTestServer(t, nil) // newTestServer wires no system engine

	start := doJSON(t, http.MethodPost, httpSrv.URL+"/api/character-creation/start",
		map[string]any{"mode": "quick", "name": "Bram"}, "")
	if start.StatusCode != http.StatusBadRequest {
		t.Errorf("start without an engine: status = %d, want 400", start.StatusCode)
	}

	answer := doJSON(t, http.MethodPost, httpSrv.URL+"/api/character-creation/answer",
		map[string]any{"session_id": "abc", "answer": "elf"}, "")
	if answer.StatusCode != http.StatusBadRequest {
		t.Errorf("answer without an engine: status = %d, want 400", answer.StatusCode)
	}
}

func TestCharacterCreation_BadRequests(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)

	cases := []struct {
		name, path string
		body       any
	}{
		{"start no name", "/api/character-creation/start", map[string]any{"mode": "quick"}},
		{"start bad mode", "/api/character-creation/start", map[string]any{"mode": "wizardly", "name": "Bram"}},
		{"answer no session", "/api/character-creation/answer", map[string]any{"answer": "elf"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, http.MethodPost, httpSrv.URL+c.path, c.body, "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestCharacterCreation_CrossOrigin_Rejected(t *testing.T) {
	_, httpSrv := newTestServer(t, nil)
	resp := doJSON(t, http.MethodPost, httpSrv.URL+"/api/character-creation/start",
		map[string]any{"mode": "quick", "name": "Bram"}, "http://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}
