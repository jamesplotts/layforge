// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package transcription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// WhisperProvider implements Provider (design doc §4) against a
// self-hosted Whisper-family server speaking the OpenAI
// /v1/audio/transcriptions contract — see this package's own doc
// comment for why that contract, not a specific product, is the actual
// interface.
type WhisperProvider struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

var _ Provider = (*WhisperProvider)(nil)

// NewWhisperProvider creates a WhisperProvider calling the server at
// baseURL (e.g. "http://localhost:9000"; no trailing slash), passing
// model as the request's "model" form field — most self-hosted servers
// use this to pick which loaded Whisper model size/variant answers the
// request, the same role "model" plays in an Ollama completion request.
func NewWhisperProvider(baseURL, model string) *WhisperProvider {
	return &WhisperProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		// 60s, not the 15-30s timeouts elsewhere in this codebase: a
		// real ASR pass over a multi-second recording on CPU-only
		// hardware can genuinely take longer than a single LLM/image-gen
		// HTTP round trip.
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// filenameForMimeType derives a plausible filename extension from
// mimeType so the server's own format sniffing (most such servers shell
// out to ffmpeg, which keys off the extension) has something to work
// with — e.g. "audio/webm;codecs=opus" -> "audio.webm". Falls back to
// ".webm" (a browser MediaRecorder's typical default) for anything
// unrecognized, rather than failing the request over a format Master
// itself has no need to understand.
func filenameForMimeType(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	switch strings.TrimSpace(base) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "audio.wav"
	case "audio/mp4", "audio/aac", "audio/x-m4a":
		return "audio.m4a"
	case "audio/mpeg", "audio/mp3":
		return "audio.mp3"
	case "audio/ogg":
		return "audio.ogg"
	default:
		return "audio.webm"
	}
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

// Transcribe implements Provider. It uploads audio as a multipart form
// file (the "file" field, matching the OpenAI contract) alongside a
// "model" field, and parses the response's "text" field.
func (p *WhisperProvider) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, filenameForMimeType(mimeType))},
		"Content-Type":        {mimeType},
	})
	if err != nil {
		return "", fmt.Errorf("transcription: building multipart file part: %w", err)
	}
	if _, err := filePart.Write(audio); err != nil {
		return "", fmt.Errorf("transcription: writing audio bytes: %w", err)
	}
	if err := writer.WriteField("model", p.model); err != nil {
		return "", fmt.Errorf("transcription: writing model field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("transcription: closing multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("transcription: building request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription: calling whisper server (is it running at %s?): %w", p.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("transcription: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcription: whisper server returned %s: %s", resp.Status, string(respBody))
	}

	var parsed transcriptionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("transcription: parsing response: %w", err)
	}
	return parsed.Text, nil
}
