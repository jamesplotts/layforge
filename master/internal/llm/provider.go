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

// CompletionRequest is a single-turn (system + user) completion request.
// It deliberately doesn't support multi-turn history, streaming, or tool
// calling yet — none of those are needed for the narrative-transform
// pipeline's fast pass (design doc §7), the only caller so far, and
// adding them speculatively before a second caller exists would be
// guessing at a shape instead of learning it from real use.
type CompletionRequest struct {
	// Model names the model to use, in whatever form the Provider
	// expects (e.g. an Ollama model tag like "qwen3.8:27b"). Required.
	Model string
	// SystemPrompt sets the model's behavior/role for this call. May be
	// empty for a provider/model that doesn't need one.
	SystemPrompt string
	// UserPrompt is the content to complete against. Required.
	UserPrompt string
}

// CompletionResponse is a Provider's answer to a CompletionRequest.
type CompletionResponse struct {
	// Text is the model's response, with any provider-specific
	// reasoning/thinking channel already stripped out by the Provider
	// implementation — callers only ever see the actual answer.
	Text string
}

// Provider generates text completions for Master's narrative-transform
// pipeline (design doc §7) and any future model-backed feature built
// against this same contract.
type Provider interface {
	// Complete generates a completion for req. It returns
	// ErrEmptyCompletion if the model responded successfully but with no
	// usable text, and a wrapped transport/API error otherwise.
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
