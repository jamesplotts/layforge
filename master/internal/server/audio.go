// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// audioStreamBuffer accumulates one push-to-talk recording's chunks
// (design doc §4) until its Final chunk arrives. Chunks are appended in
// arrival order and trusted to already be correctly ordered — a single
// WebSocket connection delivers messages in the order they were sent,
// and AudioChunkPayload.Sequence exists on the wire for a future
// implementation that needs to detect gaps/reordering, not because this
// one does.
type audioStreamBuffer struct {
	mimeType string
	chunks   [][]byte
}

// handleAudioChunk implements audio.chunk (design doc §4): buffers
// payload until its Final chunk arrives, then transcribes the complete
// recording and replies with a single audio.transcription (IsFinal:
// true) on this same connection only — never broadcast, since a
// still-recording or freshly finalized push-to-talk isn't anyone else's
// business.
//
// This deliberately does not implement live partial transcription (the
// other half of design doc §4's "streaming push-to-talk" — "live
// partial-transcription feedback shown to the speaking player"): what
// was actually asked for is a final transcript populated into the
// player's own chat box for them to edit before sending, not a
// live-updating preview while they talk, so transcribing once, on
// Final, is the complete feature rather than a deferred subset of a
// bigger one. The wire shape (AudioTranscriptionPayload.IsFinal) still
// carries the field a future incremental-partial implementation would
// need, so adding that later is additive, not a protocol change.
//
// Similarly, this does not run its own Voice Activity Detection to trim
// silence from within the recording — the held-button window itself is
// already the speech boundary a human chose, and a self-hosted
// transcription backend is free to do its own VAD/silence handling
// internally (many do); reimplementing that in Master would be
// validation for a problem the backend already solves.
//
// Transcription runs synchronously within this call, on this
// connection's own read-loop goroutine — the same shape resolveCheck/
// importCharacter already use for their own slow external calls (a
// direct, blocking gRPC/HTTP round trip), not runSlowPass's
// launch-in-a-goroutine pattern, since nothing else is waiting on this
// connection's read loop in the meantime the way other players are
// waiting on the DM's turn.
func (s *Server) handleAudioChunk(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string, payload protocol.AudioChunkPayload) error {
	if s.transcription == nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("voice transcription is not configured on this Master"))
	}
	if payload.StreamID == "" {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("stream_id is required"))
	}

	chunk, err := base64.StdEncoding.DecodeString(payload.AudioBase64)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("audio_base64 does not decode: %w", err))
	}
	s.appendAudioChunk(payload.StreamID, payload.MimeType, chunk)

	if !payload.Final {
		return nil
	}

	audio, mimeType := s.takeAudioStream(payload.StreamID)
	text, err := s.transcription.Transcribe(ctx, audio, mimeType)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("transcription failed: %w", err))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeAudioTranscription, protocol.AudioTranscriptionPayload{
		StreamID: payload.StreamID,
		Text:     text,
		IsFinal:  true,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing audio.transcription: %w", err)
	}
	return nil
}

// appendAudioChunk records data as the next chunk of streamID's
// in-progress recording, creating its buffer on first use.
func (s *Server) appendAudioChunk(streamID, mimeType string, data []byte) {
	s.audioStreamsMu.Lock()
	defer s.audioStreamsMu.Unlock()
	buf, ok := s.audioStreams[streamID]
	if !ok {
		buf = &audioStreamBuffer{}
		s.audioStreams[streamID] = buf
	}
	buf.mimeType = mimeType
	buf.chunks = append(buf.chunks, data)
}

// takeAudioStream removes and returns streamID's buffered audio,
// concatenated in arrival order, along with its recorded mime type.
// Returns (nil, "") if streamID has no buffer — not expected in
// practice, since handleAudioChunk always appends the Final chunk's own
// bytes (via appendAudioChunk) before calling this.
func (s *Server) takeAudioStream(streamID string) ([]byte, string) {
	s.audioStreamsMu.Lock()
	defer s.audioStreamsMu.Unlock()
	buf, ok := s.audioStreams[streamID]
	if !ok {
		return nil, ""
	}
	delete(s.audioStreams, streamID)

	var total int
	for _, c := range buf.chunks {
		total += len(c)
	}
	audio := make([]byte, 0, total)
	for _, c := range buf.chunks {
		audio = append(audio, c...)
	}
	return audio, buf.mimeType
}
