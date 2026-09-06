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

func newFakeOpenAICompatible(t *testing.T, respond func(w http.ResponseWriter, req map[string]any, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected request path %q, want /chat/completions", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		respond(w, req, r)
	}))
}

func TestOpenAICompatibleProvider_Complete_ReturnsTrimmedText(t *testing.T) {
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "  He drew his sword.  "}},
			},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "test-key", nil)
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

func TestOpenAICompatibleProvider_Complete_SendsBearerAuthHeader(t *testing.T) {
	var gotAuth string
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "sk-test-123", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if want := "Bearer sk-test-123"; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

func TestOpenAICompatibleProvider_Complete_SendsSystemAndUserMessagesInOrder(t *testing.T) {
	var gotMessages []any
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "test-key", nil)
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
	if first["role"] != "system" || first["content"] != "You are a narrator." {
		t.Errorf("messages[0] = %+v, want system/You are a narrator.", first)
	}
	second := gotMessages[1].(map[string]any)
	if second["role"] != "user" || second["content"] != "I draw my sword." {
		t.Errorf("messages[1] = %+v, want user/I draw my sword.", second)
	}
}

// TestOpenAICompatibleProvider_Complete_NormalizesStringEncodedToolArguments
// covers the one real difference from Ollama's own wire shape: OpenAI's
// convention returns a tool call's function.arguments as a JSON-encoded
// string, not a nested object. server's tool-dispatch code (dm_tools.go,
// character_review.go) calls json.Unmarshal directly on ToolCall.Arguments
// assuming it's already an object — this provider must normalize the
// string into raw object bytes before returning ToolCall, or every tool
// call from this provider would fail to parse downstream.
func TestOpenAICompatibleProvider_Complete_NormalizesStringEncodedToolArguments(t *testing.T) {
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "resolve_check",
									"arguments": `{"character_id":"pc-1","dc":15}`,
								},
							},
						},
					},
				},
			},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "test-key", nil)
	got, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "I attack.",
		Tools:      []llm.Tool{{Name: "resolve_check", Description: "resolve a check"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(got.ToolCalls))
	}
	tc := got.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "resolve_check" {
		t.Errorf("ToolCall = %+v, want ID=call_1 Name=resolve_check", tc)
	}
	var args struct {
		CharacterID string `json:"character_id"`
		DC          int    `json:"dc"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("Arguments did not unmarshal as a JSON object: %v (raw: %s)", err, tc.Arguments)
	}
	if args.CharacterID != "pc-1" || args.DC != 15 {
		t.Errorf("Arguments = %+v, want CharacterID=pc-1 DC=15", args)
	}
}

func TestOpenAICompatibleProvider_Complete_NonOKStatus_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "bad-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err == nil {
		t.Fatal("Complete() error = nil, want non-nil")
	}
}

func TestOpenAICompatibleProvider_Complete_EmptyCompletion_ReturnsErrEmptyCompletion(t *testing.T) {
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": ""}}},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "test-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if !errors.Is(err, llm.ErrEmptyCompletion) {
		t.Errorf("Complete() error = %v, want ErrEmptyCompletion", err)
	}
}

func TestOpenAICompatibleProvider_Complete_SendsToolsInFunctionShape(t *testing.T) {
	var gotTools []any
	ts := newFakeOpenAICompatible(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotTools = req["tools"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	})
	defer ts.Close()

	p := llm.NewOpenAICompatibleProvider(ts.URL, "test-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:      "test-model",
		UserPrompt: "hi",
		Tools: []llm.Tool{
			{Name: "resolve_check", Description: "resolve a check", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(gotTools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(gotTools))
	}
	tool := gotTools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tools[0].type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "resolve_check" {
		t.Errorf("tools[0].function.name = %v, want resolve_check", fn["name"])
	}
}
