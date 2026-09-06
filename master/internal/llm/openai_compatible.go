// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatibleProvider is a Provider backed by any server speaking
// the OpenAI Chat Completions wire format
// (POST {baseURL}/chat/completions, Bearer auth) — OpenAI, OpenRouter,
// and Z.ai all publish this same contract, so one implementation serves
// all three (see NewProvider's ProviderKindOpenAI/OpenRouter/ZAI cases);
// only baseURL differs between them.
//
// The one real difference from OllamaProvider's own wire shape: this
// convention returns a tool call's function.arguments as a JSON-encoded
// string, not a nested object (Ollama's own doc comment flags this as
// the one place its shape was confirmed to differ). Complete normalizes
// that string back into raw object bytes before returning ToolCall, since
// package server's tool-dispatch code unmarshals ToolCall.Arguments
// directly as an object.
type OpenAICompatibleProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

var _ Provider = (*OpenAICompatibleProvider)(nil)

// NewOpenAICompatibleProvider creates an OpenAICompatibleProvider talking
// to baseURL (e.g. "https://api.openai.com/v1") with apiKey sent as a
// Bearer token. httpClient may be nil, in which case a client with a
// generous timeout is used, matching NewOllamaProvider's own reasoning.
func NewOpenAICompatibleProvider(baseURL, apiKey string, httpClient *http.Client) *OpenAICompatibleProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &OpenAICompatibleProvider{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: httpClient}
}

type openAIToolCallFunction struct {
	Name string `json:"name"`
	// Arguments is a JSON-encoded string on the wire (this format's own
	// convention, unlike Ollama's genuine nested object) — kept as a raw
	// string here and normalized to an object in Complete.
	Arguments string `json:"arguments"`
}

type openAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
	Tools    []openAITool        `json:"tools,omitempty"`
	Stream   bool                `json:"stream"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider by calling {baseURL}/chat/completions.
func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	messages := openAIMessagesFrom(req)

	var tools []openAITool
	if len(req.Tools) > 0 {
		tools = make([]openAITool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = openAITool{
				Type:     "function",
				Function: openAIToolFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
			}
		}
	}

	body, err := json.Marshal(openAIChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	})
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: marshaling openai-compatible request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: building openai-compatible request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: calling openai-compatible endpoint: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("llm: openai-compatible endpoint returned status %d", httpResp.StatusCode)
	}

	var resp openAIChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: decoding openai-compatible response: %w", err)
	}
	if resp.Error != nil {
		return CompletionResponse{}, fmt.Errorf("llm: openai-compatible endpoint error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("llm: openai-compatible response had no choices")
	}
	message := resp.Choices[0].Message

	var toolCalls []ToolCall
	if len(message.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, len(message.ToolCalls))
		for i, tc := range message.ToolCalls {
			args, err := normalizeOpenAIToolArguments(tc.Function.Arguments)
			if err != nil {
				return CompletionResponse{}, fmt.Errorf("llm: decoding tool call arguments: %w", err)
			}
			toolCalls[i] = ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args}
		}
	}

	text := strings.TrimSpace(message.Content)
	if text == "" && len(toolCalls) == 0 {
		return CompletionResponse{}, ErrEmptyCompletion
	}
	return CompletionResponse{Text: text, ToolCalls: toolCalls}, nil
}

// normalizeOpenAIToolArguments converts a tool call's function.arguments
// — a JSON-encoded string on the wire, per this format's own convention
// — into the raw JSON object bytes ToolCall.Arguments documents. An empty
// string (a tool call with no arguments) normalizes to an empty object,
// since package server's tool-dispatch code expects a JSON object it can
// unmarshal, not an empty string.
func normalizeOpenAIToolArguments(raw string) (json.RawMessage, error) {
	if raw == "" {
		return json.RawMessage(`{}`), nil
	}
	var probe json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("arguments %q is not valid JSON: %w", raw, err)
	}
	return probe, nil
}

// openAIMessagesFrom builds the message list to send — mirrors
// ollamaMessagesFrom's own logic exactly, since CompletionRequest's
// simple-vs-multi-turn shape is provider-agnostic.
func openAIMessagesFrom(req CompletionRequest) []openAIChatMessage {
	if len(req.Messages) == 0 {
		messages := make([]openAIChatMessage, 0, 2)
		if req.SystemPrompt != "" {
			messages = append(messages, openAIChatMessage{Role: "system", Content: req.SystemPrompt})
		}
		return append(messages, openAIChatMessage{Role: "user", Content: req.UserPrompt})
	}

	messages := make([]openAIChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = openAIChatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) == 0 {
			continue
		}
		messages[i].ToolCalls = make([]openAIToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			messages[i].ToolCalls[j] = openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openAIToolCallFunction{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			}
		}
	}
	return messages
}
