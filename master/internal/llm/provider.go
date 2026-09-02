// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package llm defines Master's LLM-provider contract and its first
// implementation (Ollama). This is the seam design doc §3.1 refers to
// when it says Master "holds all LLM provider credentials" — today
// that's just an Ollama base URL on the LAN, no credentials at all, but
// the interface is shaped so a future OpenRouter/Claude/OpenAI-backed
// Provider is a new implementation of this same contract, not a
// different call pattern threaded through the rest of Master.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Errors a Provider implementation should return (wrapped, so
// errors.Is still matches) for conditions callers may want to
// distinguish from an arbitrary transport failure.
var (
	// ErrEmptyCompletion is returned when the model produced no usable
	// text at all — as opposed to a request/transport failure.
	ErrEmptyCompletion = errors.New("llm: model returned an empty completion")
)

// Role identifies who or what produced a Message in a multi-turn
// conversation — design doc §8's DM tool-use loop is the first caller
// that needs more than a single system+user turn (the narrative-
// transform pipeline's fast pass, design doc §7, still just uses
// CompletionRequest's SystemPrompt/UserPrompt shape). The zero value,
// RoleUnspecified, is never valid on a Message a caller sends — see
// IsValid. This is the Go translation of the Unspecified/LastValue
// enum-sentinel pattern from design doc §12 (see CLAUDE.md).
type Role string

const (
	RoleUnspecified Role = ""
	RoleSystem      Role = "system"
	RoleUser        Role = "user"
	RoleAssistant   Role = "assistant"
	RoleTool        Role = "tool"
)

// IsValid reports whether r is a recognized role. It deliberately
// returns false for RoleUnspecified.
func (r Role) IsValid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// Tool describes one function the model may call (design doc §8).
type Tool struct {
	Name        string
	Description string
	// Parameters is a JSON Schema (draft 2020-12) object describing this
	// tool's arguments — the same schema shape the System Engine's own
	// GetCharacterSchema publishes (design doc §6.1), e.g.
	// {"type":"object","properties":{...},"required":[...]}.
	Parameters json.RawMessage
}

// ToolCall is one tool invocation the model requested.
type ToolCall struct {
	// ID correlates a follow-up RoleTool Message back to this specific
	// call — required when a turn contains more than one ToolCall, so a
	// caller replying to several calls in one turn can address each
	// correctly. Providers that don't assign one may leave it empty.
	ID   string
	Name string
	// Arguments is the raw JSON object the model produced for this
	// call, matching the called Tool's Parameters shape.
	Arguments json.RawMessage
}

// Message is one turn in a multi-turn conversation (design doc §8's DM
// tool-use loop).
type Message struct {
	Role    Role
	Content string
	// ToolCalls is set on a RoleAssistant message that itself requested
	// tool calls, so a caller can replay a full turn history back to the
	// model on the next Complete call.
	ToolCalls []ToolCall
	// ToolCallID correlates a RoleTool message back to the ToolCall.ID
	// it's answering. Required (by convention; not enforced by this
	// package) when Role is RoleTool.
	ToolCallID string
}

// CompletionRequest is a completion request. The simple case — a single
// system+user turn, no tools — is the narrative-transform pipeline's
// fast pass's only need (design doc §7): set SystemPrompt/UserPrompt and
// leave Messages/Tools empty. A caller building a multi-turn tool-use
// conversation (design doc §8) sets Messages instead, constructing the
// full turn history itself (including any prior RoleAssistant/RoleTool
// turns) — SystemPrompt/UserPrompt are ignored whenever Messages is
// non-empty.
type CompletionRequest struct {
	// Model names the model to use, in whatever form the Provider
	// expects (e.g. an Ollama model tag like "qwen3.8:27b"). Required.
	Model string
	// SystemPrompt sets the model's behavior/role for this call. May be
	// empty for a provider/model that doesn't need one. Ignored when
	// Messages is non-empty.
	SystemPrompt string
	// UserPrompt is the content to complete against. Ignored when
	// Messages is non-empty.
	UserPrompt string
	// Messages, if non-empty, replaces SystemPrompt/UserPrompt with a
	// full multi-turn conversation — see the type doc comment.
	Messages []Message
	// Tools, if non-empty, are offered to the model as callable
	// functions (design doc §8) — the model may respond with
	// CompletionResponse.ToolCalls instead of (or before) text. Empty
	// for the fast pass, which has no tools.
	Tools []Tool
}

// CompletionResponse is a Provider's answer to a CompletionRequest.
type CompletionResponse struct {
	// Text is the model's response, with any provider-specific
	// reasoning/thinking channel already stripped out by the Provider
	// implementation — callers only ever see the actual answer. May be
	// empty when ToolCalls is set (a pure tool-call turn); some
	// providers/models also return narration alongside tool calls, in
	// which case both are set.
	Text string
	// ToolCalls holds any tool invocations the model requested — set
	// only when CompletionRequest.Tools was non-empty and the model
	// chose to call one or more.
	ToolCalls []ToolCall
}

// Provider generates text completions for Master's narrative-transform
// pipeline (design doc §7), the DM tool-use loop (design doc §8), and
// any future model-backed feature built against this same contract.
type Provider interface {
	// Complete generates a completion for req. It returns
	// ErrEmptyCompletion if the model responded successfully but with
	// neither usable text nor a tool call, and a wrapped transport/API
	// error otherwise.
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
