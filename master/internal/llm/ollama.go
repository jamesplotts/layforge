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
// endpoint (https://github.com/ollama/ollama/blob/main/docs/api.md).
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

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
		// Thinking holds a reasoning-model's separate chain-of-thought
		// channel (present on e.g. qwen3.8:27b, absent on models without
		// a "thinking" capability). Deliberately not surfaced on
		// CompletionResponse — see that type's doc comment.
		Thinking string `json:"thinking"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// Complete implements Provider by calling Ollama's /api/chat with
// stream: false.
func (p *OllamaProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	messages := make([]ollamaChatMessage, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, ollamaChatMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, ollamaChatMessage{Role: "user", Content: req.UserPrompt})

	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
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
	// untrustworthy rather than returning what may be partial content.
	if !resp.Done {
		return CompletionResponse{}, fmt.Errorf("llm: ollama response incomplete (done=false)")
	}

	text := strings.TrimSpace(resp.Message.Content)
	if text == "" {
		return CompletionResponse{}, ErrEmptyCompletion
	}
	return CompletionResponse{Text: text}, nil
}
