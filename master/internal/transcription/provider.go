// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package transcription implements design doc §4's push-to-talk
// transcription backend as a pluggable interface — the same shape as
// package llm's Provider (design doc §3.1) and package imagegen's
// Provider (§6.3): Master depends only on this interface, never on a
// specific ASR backend, so a self-hoster can substitute a different
// transcription service without touching package server.
//
// WhisperProvider (whisper.go) is the reference implementation, calling
// a self-hosted Whisper-family server that speaks the OpenAI
// /v1/audio/transcriptions contract (faster-whisper-server,
// openai-whisper-asr-webservice, LocalAI, and OpenAI's own hosted API
// all implement this same request/response shape) — the same
// "self-hosted service Master calls over HTTP" pattern already
// established for narrative rendering (Ollama) and image generation
// (ComfyUI), not a new architecture. Master never embeds an ASR model
// or a C/cgo dependency directly (see CLAUDE.md's "single static
// binary, no runtime dependency" rule for Master itself) — the model
// runs entirely in the operator's own configured service.
package transcription

import "context"

// Provider transcribes one complete audio recording (design doc §4).
// Implementations are expected to be slow relative to a WebSocket
// round-trip (real ASR inference, at least hundreds of milliseconds) —
// see internal/server/audio.go for how callers handle that.
type Provider interface {
	// Transcribe returns the spoken text in audio, whose bytes are
	// encoded as mimeType names (e.g. "audio/webm;codecs=opus") —
	// implementations forward mimeType to the backend rather than
	// assuming a fixed format, since that's a client/browser choice.
	Transcribe(ctx context.Context, audio []byte, mimeType string) (text string, err error)
}
