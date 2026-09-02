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

// OllamaProvider is a Provider backed by an Ollama server's /api/chat
// endpoint (https://github.com/ollama/ollama/blob/main/docs/api.md). Its
// request/response tool-calling shape (ollamaTool/ollamaToolCall below)
// was confirmed against a real Ollama server and a real tool-capable
// model (qwen2.5:32b) — notably that Arguments comes back as a genuine
// JSON object, not an OpenAI-style JSON-encoded string — not assumed
// from documentation alone.
type OllamaProvider struct {
	baseURL string
	client  *http.Client
}

var _ Provider = (*OllamaProvider)(nil)

// NewOllamaProvider creates an OllamaProvider talking to the Ollama
// server at baseURL (e.g. "http://192.168.1.56:11434"). httpClient may
// be nil, in which case a client with a generous timeout is used —
// Ollama's first response for a model not already loaded into VRAM can
// take several seconds just to load, before generation even starts.
func NewOllamaProvider(baseURL string, httpClient *http.Client) *OllamaProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &OllamaProvider{baseURL: strings.TrimRight(baseURL, "/"), client: httpClient}
}

// ollamaToolCallFunction is the "function" object inside an
// ollamaToolCall, used both when Ollama reports a call it wants made
// (response) and when replaying that same call back as conversation
// history (request) — same shape either direction.
type ollamaToolCallFunction struct {
	Name string `json:"name"`
	// Arguments is kept as a raw JSON object rather than unmarshaled
	// further: Master's own use (package server's DM tool dispatch)
	// parses it into whatever shape a specific tool expects, and this
	// package has no business knowing tool-specific argument shapes.
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaToolCall struct {
	// ID is omitted on outbound messages that don't have one (e.g. a
	// tool call built without a provider-assigned ID); Ollama has always
	// supplied one in real testing against a real server, but ToolCall.ID
	// documents that a provider isn't required to.
	ID       string                 `json:"id,omitempty"`
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaChatMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	// ToolCallID is set on a "tool"-role message to say which prior
	// ToolCall.ID this is the result of.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type ollamaToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ollamaTool is Ollama's wire shape for one offered tool — "type":
// "function" is the only tool type Ollama (or the OpenAI-compatible
// convention it follows) currently defines.
type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Tools    []ollamaTool        `json:"tools,omitempty"`
	Stream   bool                `json:"stream"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
		// Thinking holds a reasoning-model's separate chain-of-thought
		// channel (present on e.g. qwen3.8:27b, absent on models without
		// a "thinking" capability). Deliberately not surfaced on
		// CompletionResponse — see that type's doc comment.
		Thinking  string           `json:"thinking"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// Complete implements Provider by calling Ollama's /api/chat with
// stream: false.
func (p *OllamaProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	messages := ollamaMessagesFrom(req)

	var tools []ollamaTool
	if len(req.Tools) > 0 {
		tools = make([]ollamaTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = ollamaTool{
				Type:     "function",
				Function: ollamaToolFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
			}
		}
	}

	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	})
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: marshaling ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: building ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: calling ollama: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("llm: ollama returned status %d", httpResp.StatusCode)
	}

	var resp ollamaChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm: decoding ollama response: %w", err)
	}
	if resp.Error != "" {
		return CompletionResponse{}, fmt.Errorf("llm: ollama error: %s", resp.Error)
	}
	// Observed in manual testing against this server: a request with
	// stream: false can still come back with done: false. Treat that as
	// untrustworthy rather than returning what may be partial content —
	// confirmed this holds for both a plain-text response and a tool-call
	// response (both report done: true in the success case).
	if !resp.Done {
		return CompletionResponse{}, fmt.Errorf("llm: ollama response incomplete (done=false)")
	}

	var toolCalls []ToolCall
	if len(resp.Message.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, len(resp.Message.ToolCalls))
		for i, tc := range resp.Message.ToolCalls {
			toolCalls[i] = ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}
		}
	}

	text := strings.TrimSpace(resp.Message.Content)
	if text == "" && len(toolCalls) == 0 {
		return CompletionResponse{}, ErrEmptyCompletion
	}
	return CompletionResponse{Text: text, ToolCalls: toolCalls}, nil
}

// ollamaMessagesFrom builds the message list to send: req.Messages
// verbatim if the caller supplied a multi-turn conversation, otherwise
// the simple system+user shape built from SystemPrompt/UserPrompt — see
// CompletionRequest's doc comment for which one wins.
func ollamaMessagesFrom(req CompletionRequest) []ollamaChatMessage {
	if len(req.Messages) == 0 {
		messages := make([]ollamaChatMessage, 0, 2)
		if req.SystemPrompt != "" {
			messages = append(messages, ollamaChatMessage{Role: "system", Content: req.SystemPrompt})
		}
		return append(messages, ollamaChatMessage{Role: "user", Content: req.UserPrompt})
	}

	messages := make([]ollamaChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = ollamaChatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) == 0 {
			continue
		}
		messages[i].ToolCalls = make([]ollamaToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			messages[i].ToolCalls[j] = ollamaToolCall{ID: tc.ID, Function: ollamaToolCallFunction{Name: tc.Name, Arguments: tc.Arguments}}
		}
	}
	return messages
}
