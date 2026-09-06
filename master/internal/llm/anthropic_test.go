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

func newFakeAnthropic(t *testing.T, respond func(w http.ResponseWriter, req map[string]any, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("unexpected request path %q, want /messages", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		respond(w, req, r)
	}))
}

func TestAnthropicProvider_Complete_ReturnsConcatenatedText(t *testing.T) {
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "He drew his sword."},
			},
		})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
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

func TestAnthropicProvider_Complete_SendsAuthHeaders(t *testing.T) {
	var gotKey, gotVersion string
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "sk-ant-test", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if gotKey != "sk-ant-test" {
		t.Errorf("x-api-key = %q, want sk-ant-test", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version header was not set")
	}
}

func TestAnthropicProvider_Complete_ExtractsSystemPromptToTopLevelField(t *testing.T) {
	var gotSystem any
	var gotMessages []any
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotSystem = req["system"]
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model:        "test-model",
		SystemPrompt: "You are a narrator.",
		UserPrompt:   "I draw my sword.",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if gotSystem != "You are a narrator." {
		t.Errorf("system = %v, want %q", gotSystem, "You are a narrator.")
	}
	if len(gotMessages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 (system must not appear as a message)", len(gotMessages))
	}
	msg := gotMessages[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("messages[0].role = %v, want user", msg["role"])
	}
}

func TestAnthropicProvider_Complete_SendsToolsAsInputSchema(t *testing.T) {
	var gotTools []any
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotTools = req["tools"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
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
	if tool["name"] != "resolve_check" {
		t.Errorf("tools[0].name = %v, want resolve_check", tool["name"])
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Error("tools[0] has no input_schema field")
	}
	if _, ok := tool["parameters"]; ok {
		t.Error("tools[0] should not have a parameters field (that's the OpenAI-shape name, not Anthropic's)")
	}
}

func TestAnthropicProvider_Complete_ToolUseRoundTrip(t *testing.T) {
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "resolve_check",
					"input": map[string]any{"character_id": "pc-1", "dc": 15},
				},
			},
		})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
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
	if tc.ID != "toolu_1" || tc.Name != "resolve_check" {
		t.Errorf("ToolCall = %+v, want ID=toolu_1 Name=resolve_check", tc)
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

func TestAnthropicProvider_Complete_ToolResultMappedToUserRoleMessage(t *testing.T) {
	var gotMessages []any
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		gotMessages = req["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "I attack."},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "resolve_check", Arguments: json.RawMessage(`{"dc":15}`)}}},
			{Role: llm.RoleTool, ToolCallID: "toolu_1", Content: "Success"},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(gotMessages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(gotMessages))
	}

	assistantMsg := gotMessages[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("messages[1].role = %v, want assistant", assistantMsg["role"])
	}
	assistantContent := assistantMsg["content"].([]any)
	foundToolUse := false
	for _, block := range assistantContent {
		b := block.(map[string]any)
		if b["type"] == "tool_use" {
			foundToolUse = true
			if b["id"] != "toolu_1" || b["name"] != "resolve_check" {
				t.Errorf("tool_use block = %+v, want id=toolu_1 name=resolve_check", b)
			}
		}
	}
	if !foundToolUse {
		t.Error("assistant message content has no tool_use block")
	}

	toolResultMsg := gotMessages[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("messages[2].role = %v, want user (Anthropic has no tool role)", toolResultMsg["role"])
	}
	toolResultContent := toolResultMsg["content"].([]any)
	if len(toolResultContent) != 1 {
		t.Fatalf("len(messages[2].content) = %d, want 1", len(toolResultContent))
	}
	block := toolResultContent[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_result block = %+v, want type=tool_result tool_use_id=toolu_1", block)
	}
}

func TestAnthropicProvider_Complete_NonOKStatus_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "bad-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err == nil {
		t.Fatal("Complete() error = nil, want non-nil")
	}
}

func TestAnthropicProvider_Complete_EmptyCompletion_ReturnsErrEmptyCompletion(t *testing.T) {
	ts := newFakeAnthropic(t, func(w http.ResponseWriter, req map[string]any, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{}})
	})
	defer ts.Close()

	p := llm.NewAnthropicProvider(ts.URL, "test-key", nil)
	_, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if !errors.Is(err, llm.ErrEmptyCompletion) {
		t.Errorf("Complete() error = %v, want ErrEmptyCompletion", err)
	}
}
