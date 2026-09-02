// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/llm"
)

// newFakeOllama starts an httptest.Server that plays Ollama's /api/chat
// role, decoding the request and handing it to respond so each test can
// script exactly what comes back — hermetic, no dependency on a real
// Ollama server being reachable.
func newFakeOllama(t *testing.T, respond func(w http.ResponseWriter, req map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected request path %q, want /api/chat", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		respond(w, req)
	}))
}

func TestOllamaProvider_Complete_ReturnsTrimmedText(t *testing.T) {
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   req["model"],
			"message": map[string]any{"role": "assistant", "content": "  He drew his sword.  "},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	got, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "I draw my sword.",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got.Text != "He drew his sword." {
		t.Errorf("Text = %q, want %q", got.Text, "He drew his sword.")
	}
}

func TestOllamaProvider_Complete_SendsSystemAndUserMessagesInOrder(t *testing.T) {
	var gotMessages []any
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "ok"},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:        "test-model",
		SystemPrompt: "You are a narrator.",
		UserPrompt:   "I draw my sword.",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(gotMessages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(gotMessages))
	}
	first := gotMessages[0].(map[string]any)
	second := gotMessages[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are a narrator." {
		t.Errorf("messages[0] = %+v, want system prompt first", first)
	}
	if second["role"] != "user" || second["content"] != "I draw my sword." {
		t.Errorf("messages[1] = %+v, want user prompt second", second)
	}
}

func TestOllamaProvider_Complete_NoSystemPrompt_SendsOnlyUserMessage(t *testing.T) {
	var gotMessages []any
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "ok"},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "hello",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(gotMessages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 (no system prompt given)", len(gotMessages))
	}
}

func TestOllamaProvider_Complete_EmptyContent_ReturnsErrEmptyCompletion(t *testing.T) {
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "   "},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if !errors.Is(err, llm.ErrEmptyCompletion) {
		t.Errorf("Complete() error = %v, want ErrEmptyCompletion", err)
	}
}

func TestOllamaProvider_Complete_DoneFalse_ReturnsError(t *testing.T) {
	// Observed against the real server: stream: false can still come
	// back with done: false. Complete must not treat that as success.
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "partial garbage"},
			"done":    false,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err == nil {
		t.Fatal("Complete() succeeded, want an error for done=false")
	}
}

func TestOllamaProvider_Complete_OllamaErrorField_ReturnsError(t *testing.T) {
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "model \"nonexistent\" not found",
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "nonexistent", UserPrompt: "hi"})
	if err == nil {
		t.Fatal("Complete() succeeded, want an error")
	}
}

func TestOllamaProvider_Complete_WithTools_SendsToolDefinitions(t *testing.T) {
	var gotTools []any
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		gotTools = req["tools"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "ok"},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "hi",
		Tools: []llm.Tool{{
			Name:        "resolve_check",
			Description: "Resolve a check.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"ability":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(gotTools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(gotTools))
	}
	tool := gotTools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tools[0].type = %v, want %q", tool["type"], "function")
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "resolve_check" {
		t.Errorf("tools[0].function.name = %v, want %q", fn["name"], "resolve_check")
	}
	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("tools[0].function.parameters.type = %v, want %q", params["type"], "object")
	}
}

func TestOllamaProvider_Complete_ResponseWithToolCalls_PopulatesToolCallsNotText(t *testing.T) {
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{
					{
						"id": "call_abc123",
						"function": map[string]any{
							"name":      "resolve_check",
							"arguments": map[string]any{"ability": "Strength", "character_id": "char-1"},
						},
					},
				},
			},
			"done": true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	got, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "I try to climb the wall.",
		Tools:      []llm.Tool{{Name: "resolve_check"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got.Text != "" {
		t.Errorf("Text = %q, want empty (a pure tool-call turn)", got.Text)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(got.ToolCalls))
	}
	call := got.ToolCalls[0]
	if call.ID != "call_abc123" || call.Name != "resolve_check" {
		t.Errorf("ToolCalls[0] = %+v, want ID=call_abc123 Name=resolve_check", call)
	}
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatalf("unmarshaling Arguments error = %v", err)
	}
	if args["ability"] != "Strength" || args["character_id"] != "char-1" {
		t.Errorf("Arguments = %+v, want ability=Strength character_id=char-1", args)
	}
}

func TestOllamaProvider_Complete_WithMessages_SendsToolCallAndResultInHistory(t *testing.T) {
	var gotMessages []any
	ts := newFakeOllama(t, func(w http.ResponseWriter, req map[string]any) {
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "You fail to find a grip and slip."},
			"done":    true,
		})
	})
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a DM."},
			{Role: llm.RoleUser, Content: "I climb the wall."},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "resolve_check", Arguments: json.RawMessage(`{"ability":"Strength"}`)},
				},
			},
			{Role: llm.RoleTool, Content: `{"total":8}`, ToolCallID: "call_1"},
		},
		Tools: []llm.Tool{{Name: "resolve_check"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(gotMessages) != 4 {
		t.Fatalf("len(messages) = %d, want 4 (SystemPrompt/UserPrompt must be ignored when Messages is set)", len(gotMessages))
	}
	assistantMsg := gotMessages[2].(map[string]any)
	toolCalls := assistantMsg["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("assistant message tool_calls length = %d, want 1", len(toolCalls))
	}
	toolCall := toolCalls[0].(map[string]any)
	if toolCall["id"] != "call_1" {
		t.Errorf("tool_calls[0].id = %v, want %q", toolCall["id"], "call_1")
	}

	toolResultMsg := gotMessages[3].(map[string]any)
	if toolResultMsg["role"] != "tool" || toolResultMsg["tool_call_id"] != "call_1" {
		t.Errorf("messages[3] = %+v, want role=tool tool_call_id=call_1", toolResultMsg)
	}
}

func TestOllamaProvider_Complete_NonOKStatus_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := llm.NewOllamaProvider(ts.URL, nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err == nil {
		t.Fatal("Complete() succeeded, want an error for a 500 response")
	}
}
