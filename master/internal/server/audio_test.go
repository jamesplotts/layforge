// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/transcription"
)

// fakeTranscriptionProvider is a controllable transcription.Provider —
// lastAudio/lastMimeType capture the most recent call's arguments, for
// asserting Server actually assembled and forwarded the full recording,
// not just the last chunk.
type fakeTranscriptionProvider struct {
	text string
	err  error

	lastAudio    []byte
	lastMimeType string
	callCount    int
}

var _ transcription.Provider = (*fakeTranscriptionProvider)(nil)

func (f *fakeTranscriptionProvider) Transcribe(_ context.Context, audio []byte, mimeType string) (string, error) {
	f.callCount++
	f.lastAudio = audio
	f.lastMimeType = mimeType
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

func newTestServerWithTranscription(t *testing.T, provider transcription.Provider) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, provider, nil, session.NewHub())
	return httptest.NewServer(srv.Handler())
}

func audioChunkMessage(campaignID, sender, streamID string, sequence int, audioB64 string, final bool, mimeType string) protocol.AudioChunkMessage {
	return protocol.AudioChunkMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       sender + "-chunk",
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeAudioChunk,
		},
		Payload: protocol.AudioChunkPayload{
			StreamID:    streamID,
			Sequence:    sequence,
			AudioBase64: audioB64,
			Final:       final,
			MimeType:    mimeType,
		},
	}
}

func TestServe_AudioChunk_SingleFinalChunk_TranscribesAndRepliesOnSameConnection(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "the guard is lying to us"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	audioB64 := base64.StdEncoding.EncodeToString([]byte("hello-audio"))
	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio", "player-a", "stream-1", 0, audioB64, true, "audio/webm;codecs=opus")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	var transcript protocol.AudioTranscriptionMessage
	if err := wsjson.Read(ctx, conn, &transcript); err != nil {
		t.Fatalf("Read(audio.transcription) error = %v", err)
	}
	if transcript.Payload.StreamID != "stream-1" {
		t.Errorf("StreamID = %q, want stream-1", transcript.Payload.StreamID)
	}
	if transcript.Payload.Text != "the guard is lying to us" {
		t.Errorf("Text = %q, want %q", transcript.Payload.Text, "the guard is lying to us")
	}
	if !transcript.Payload.IsFinal {
		t.Error("IsFinal = false, want true")
	}
	if string(fake.lastAudio) != "hello-audio" {
		t.Errorf("provider received audio = %q, want %q", string(fake.lastAudio), "hello-audio")
	}
	if fake.lastMimeType != "audio/webm;codecs=opus" {
		t.Errorf("provider received mimeType = %q, want audio/webm;codecs=opus", fake.lastMimeType)
	}
}

func TestServe_AudioChunk_MultipleChunks_AssemblesInOrderBeforeTranscribing(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "assembled"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-multi", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parts := []string{"one-", "two-", "three"}
	for i, part := range parts {
		final := i == len(parts)-1
		b64 := base64.StdEncoding.EncodeToString([]byte(part))
		if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-multi", "player-a", "stream-multi", i, b64, final, "audio/webm")); err != nil {
			t.Fatalf("Write(audio.chunk %d) error = %v", i, err)
		}
	}

	var transcript protocol.AudioTranscriptionMessage
	if err := wsjson.Read(ctx, conn, &transcript); err != nil {
		t.Fatalf("Read(audio.transcription) error = %v", err)
	}
	if string(fake.lastAudio) != "one-two-three" {
		t.Errorf("provider received assembled audio = %q, want %q", string(fake.lastAudio), "one-two-three")
	}
	if fake.callCount != 1 {
		t.Errorf("Transcribe called %d times, want exactly 1 (only on the Final chunk)", fake.callCount)
	}
}

func TestServe_AudioChunk_NonFinalChunk_NoReplyAndNotYetTranscribed(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "should not be used yet"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-partial", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b64 := base64.StdEncoding.EncodeToString([]byte("partial"))
	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-partial", "player-a", "stream-partial", 0, b64, false, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	// Prove no reply arrives for a non-Final chunk by sending a second,
	// unrelated message and confirming *that* reply is the first thing
	// read back — a genuine race-free way to assert "nothing else was
	// sent first" over this connection.
	if err := wsjson.Write(ctx, conn, protocol.HistoryRequestMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "hist-1",
			Timestamp:       time.Now().UTC(),
			SenderID:        "player-a",
			CampaignID:      "campaign-audio-partial",
			Type:            protocol.MessageTypeLogHistoryRequest,
		},
	}); err != nil {
		t.Fatalf("Write(log.history_request) error = %v", err)
	}
	var resp protocol.HistoryResponseMessage
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("Read(log.history_response) error = %v", err)
	}
	if fake.callCount != 0 {
		t.Errorf("Transcribe called %d times, want 0 before the Final chunk arrives", fake.callCount)
	}
}

func TestServe_AudioChunk_NotConfigured_ReturnsSystemError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(server.New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, session.NewHub()).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-unconfigured", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b64 := base64.StdEncoding.EncodeToString([]byte("audio"))
	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-unconfigured", "player-a", "stream-x", 0, b64, true, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_AudioChunk_MissingStreamID_ReturnsSystemError(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "unused"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-nostream", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b64 := base64.StdEncoding.EncodeToString([]byte("audio"))
	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-nostream", "player-a", "", 0, b64, true, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if fake.callCount != 0 {
		t.Errorf("Transcribe called %d times, want 0 for a rejected request", fake.callCount)
	}
}

func TestServe_AudioChunk_MalformedBase64_ReturnsSystemError(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "unused"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-badb64", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-badb64", "player-a", "stream-bad", 0, "not-valid-base64!!!", true, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_AudioChunk_TranscriptionProviderError_ReturnsSystemError(t *testing.T) {
	fake := &fakeTranscriptionProvider{err: context.DeadlineExceeded}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-audio-provider-err", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b64 := base64.StdEncoding.EncodeToString([]byte("audio"))
	if err := wsjson.Write(ctx, conn, audioChunkMessage("campaign-audio-provider-err", "player-a", "stream-err", 0, b64, true, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk) error = %v", err)
	}

	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_AudioChunk_TwoDistinctStreams_DoNotCrossContaminate(t *testing.T) {
	fake := &fakeTranscriptionProvider{text: "ok"}
	ts := newTestServerWithTranscription(t, fake)
	defer ts.Close()

	connA := dialAndJoin(t, ts, "campaign-audio-two-streams", "player-a")
	defer connA.CloseNow()
	connB := dialAndJoin(t, ts, "campaign-audio-two-streams", "player-b")
	defer connB.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	aB64 := base64.StdEncoding.EncodeToString([]byte("from-a"))
	bB64 := base64.StdEncoding.EncodeToString([]byte("from-b"))
	if err := wsjson.Write(ctx, connA, audioChunkMessage("campaign-audio-two-streams", "player-a", "stream-a", 0, aB64, false, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk a partial) error = %v", err)
	}
	if err := wsjson.Write(ctx, connB, audioChunkMessage("campaign-audio-two-streams", "player-b", "stream-b", 0, bB64, true, "audio/webm")); err != nil {
		t.Fatalf("Write(audio.chunk b final) error = %v", err)
	}

	var transcriptB protocol.AudioTranscriptionMessage
	if err := wsjson.Read(ctx, connB, &transcriptB); err != nil {
		t.Fatalf("Read(audio.transcription b) error = %v", err)
	}
	if string(fake.lastAudio) != "from-b" {
		t.Errorf("stream-b's transcription used audio %q, want %q (must not include stream-a's still-buffered chunk)", string(fake.lastAudio), "from-b")
	}
}
