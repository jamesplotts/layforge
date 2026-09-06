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

// anthropicAPIVersion is the anthropic-version header value every request
// must send — Anthropic versions its Messages API by this header, not by
// URL path.
const anthropicAPIVersion = "2023-06-01"

// anthropicMaxTokens is a fixed value for the required (no server-side
// default) max_tokens field. Not yet operator-configurable — nothing so
// far has needed that; would be a small additive flag if it ever does.
const anthropicMaxTokens = 4096

// AnthropicProvider is a Provider backed by Anthropic's Messages API
// (https://docs.anthropic.com/en/api/messages). Its wire shape genuinely
// differs from Ollama/OpenAI's chat-completions convention, not just in
// endpoint/auth:
//   - There is no system-role message; a system prompt is the request's
//     own top-level "system" string.
//   - Only "user"/"assistant" roles exist; a tool result rides on a
//     user-role message as a "tool_result" content block, not a separate
//     "tool" role.
//   - A tool's schema is sent under "input_schema", not "parameters".
//   - A tool call's arguments come back as a genuine JSON object under
//     "input" (like Ollama, unlike OpenAI's JSON-encoded string) — no
//     string-to-object normalization needed here.
type AnthropicProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

var _ Provider = (*AnthropicProvider)(nil)

// NewAnthropicProvider creates an AnthropicProvider talking to baseURL
// (e.g. "https://api.anthropic.com/v1") with apiKey sent as the
// x-api-key header. httpClient may be nil, in which case a client with a
// generous timeout is used, matching NewOllamaProvider's own reasoning.
func NewAnthropicProvider(baseURL, apiKey string, httpClient *http.Client) *AnthropicProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &AnthropicProvider{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: httpClient}
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	// Text is set on a "text" block.
	Text string `json:"text,omitempty"`
	// ID/Name/Input are set on a "tool_use" block (assistant-authored, in
	// a request replaying prior history, or in a response).
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// ToolUseID/Content are set on a "tool_result" block (always inside a
	// user-role message).
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	MaxTokens int                `json:"max_tokens"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider by calling {baseURL}/messages.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	system, messages := anthropicMessagesFrom(req)

	var tools []anthropicTool
	if len(req.Tools) > 0 {
		tools = make([]anthropicTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters}
		}
	}

	body, err := json.Marshal(anthropicRequest{
		Model:     req.Model,
		System:    system,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: anthropicMaxTokens,
	})
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: marshaling anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: building anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: calling anthropic: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("llm: anthropic returned status %d", httpResp.StatusCode)
	}

	var resp anthropicResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: decoding anthropic response: %w", err)
	}
	if resp.Error != nil {
		return CompletionResponse{}, fmt.Errorf("llm: anthropic error: %s", resp.Error.Message)
	}

	var textParts []string
	var toolCalls []ToolCall
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Input})
		}
	}

	text := strings.TrimSpace(strings.Join(textParts, ""))
	if text == "" && len(toolCalls) == 0 {
		return CompletionResponse{}, ErrEmptyCompletion
	}
	return CompletionResponse{Text: text, ToolCalls: toolCalls}, nil
}

// anthropicMessagesFrom builds the (system, messages) pair to send.
// Anthropic has no system-role message, so a RoleSystem message (or
// CompletionRequest.SystemPrompt in the simple case) is extracted to the
// returned system string instead of appearing in messages — see the type
// doc comment.
func anthropicMessagesFrom(req CompletionRequest) (string, []anthropicMessage) {
	if len(req.Messages) == 0 {
		return req.SystemPrompt, []anthropicMessage{
			{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: req.UserPrompt}}},
		}
	}

	var system []string
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				system = append(system, m.Content)
			}
		case RoleTool:
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content},
				},
			})
		case RoleAssistant:
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Arguments})
			}
			messages = append(messages, anthropicMessage{Role: "assistant", Content: blocks})
		default: // RoleUser and anything else falls back to a plain user turn
			messages = append(messages, anthropicMessage{
				Role:    "user",
				Content: []anthropicContentBlock{{Type: "text", Text: m.Content}},
			})
		}
	}
	return strings.Join(system, "\n\n"), messages
}
