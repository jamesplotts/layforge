// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/llm"
)

func TestNewProvider_Ollama_RequiresBaseURL(t *testing.T) {
	if _, err := llm.NewProvider(llm.ProviderConfig{Kind: llm.ProviderKindOllama}); err == nil {
		t.Error("NewProvider() error = nil, want non-nil for ollama with no base URL")
	}
	p, err := llm.NewProvider(llm.ProviderConfig{Kind: llm.ProviderKindOllama, BaseURL: "http://localhost:11434"})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if p == nil {
		t.Error("NewProvider() returned nil provider")
	}
}

func TestNewProvider_CloudKinds_RequireAPIKey(t *testing.T) {
	for _, kind := range []llm.ProviderKind{llm.ProviderKindAnthropic, llm.ProviderKindOpenAI, llm.ProviderKindOpenRouter, llm.ProviderKindZAI} {
		t.Run(string(kind), func(t *testing.T) {
			if _, err := llm.NewProvider(llm.ProviderConfig{Kind: kind}); err == nil {
				t.Errorf("NewProvider(%s) error = nil, want non-nil with no API key", kind)
			}
			p, err := llm.NewProvider(llm.ProviderConfig{Kind: kind, APIKey: "test-key"})
			if err != nil {
				t.Fatalf("NewProvider(%s) error = %v", kind, err)
			}
			if p == nil {
				t.Errorf("NewProvider(%s) returned nil provider", kind)
			}
		})
	}
}

func TestNewProvider_UnknownKind_ReturnsError(t *testing.T) {
	if _, err := llm.NewProvider(llm.ProviderConfig{Kind: llm.ProviderKind("bogus")}); err == nil {
		t.Error("NewProvider() error = nil, want non-nil for an unknown kind")
	}
	if _, err := llm.NewProvider(llm.ProviderConfig{}); err == nil {
		t.Error("NewProvider() error = nil, want non-nil for ProviderKindUnspecified")
	}
}

func TestNewProvider_BaseURLOverride_IsActuallyUsed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	}))
	defer ts.Close()

	p, err := llm.NewProvider(llm.ProviderConfig{Kind: llm.ProviderKindAnthropic, APIKey: "test-key", BaseURL: ts.URL})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	got, err := p.Complete(context.Background(), llm.CompletionRequest{Model: "test-model", UserPrompt: "hi"})
	if err != nil {
		t.Fatalf("Complete() error = %v (base URL override was not honored)", err)
	}
	if got.Text != "ok" {
		t.Errorf("Text = %q, want %q", got.Text, "ok")
	}
}

func TestProviderKind_IsValid(t *testing.T) {
	valid := []llm.ProviderKind{llm.ProviderKindOllama, llm.ProviderKindAnthropic, llm.ProviderKindOpenAI, llm.ProviderKindOpenRouter, llm.ProviderKindZAI}
	for _, k := range valid {
		if !k.IsValid() {
			t.Errorf("%s.IsValid() = false, want true", k)
		}
	}
	invalid := []llm.ProviderKind{llm.ProviderKindUnspecified, llm.ProviderKind("bogus")}
	for _, k := range invalid {
		if k.IsValid() {
			t.Errorf("%q.IsValid() = true, want false", k)
		}
	}
}
