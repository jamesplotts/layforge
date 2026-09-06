// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// partialTranscriptionInterval is how often runPartialTranscription
// re-transcribes an in-progress recording (design doc §4's "live
// partial-transcription feedback"). A tradeoff, not a measured optimum
// — short enough to feel responsive, long enough that a real ASR pass
// (hundreds of milliseconds to a few seconds on typical self-hosted
// hardware) keeps up rather than permanently falling behind: since
// runPartialTranscription's loop processes one round at a time,
// time.Ticker simply drops a tick it can't deliver while a round is
// still running, so a slow backend degrades to fewer, later partial
// updates rather than piling up overlapping Transcribe calls. A var,
// not a const, so tests can shrink it rather than waiting on the real
// interval.
var partialTranscriptionInterval = 2 * time.Second

// partialTranscriptionTimeout bounds each individual partial-pass
// Transcribe call. Shorter than WhisperProvider's own 60s HTTP client
// timeout is fine here: a partial result arriving late is simply
// skipped (the next tick tries again with more audio) — unlike the
// final pass, where the player is actively waiting on it.
const partialTranscriptionTimeout = 20 * time.Second

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
// true) — never broadcast, since a still-recording or freshly finalized
// push-to-talk isn't anyone else's business. It also starts
// runPartialTranscription (once per stream, on the first chunk) for
// design doc §4's other half — "live partial-transcription feedback
// shown to the speaking player" — see that function's own doc comment
// for how a periodic re-transcription of the growing recording delivers
// that without a new transcription backend contract.
//
// This does not run its own Voice Activity Detection to trim silence
// from within the recording — the held-button window itself is already
// the speech boundary a human chose, and a self-hosted transcription
// backend is free to do its own VAD/silence handling internally (many
// do); reimplementing that in Master would be validation for a problem
// the backend already solves.
//
// The final transcription pass runs synchronously within this call, on
// this connection's own read-loop goroutine — the same shape
// resolveCheck/importCharacter already use for their own slow external
// calls (a direct, blocking gRPC/HTTP round trip), not runSlowPass's
// launch-in-a-goroutine pattern, since nothing else is waiting on this
// connection's read loop in the meantime the way other players are
// waiting on the DM's turn. Both the final and every partial result go
// through sendToSender (never a direct connection write) — see
// runPartialTranscription's doc comment for why that specific choice is
// what actually prevents a stale partial from being delivered after the
// real final result.
func (s *Server) handleAudioChunk(ctx context.Context, conn *websocket.Conn, campaignID, senderID, inReplyTo string, payload protocol.AudioChunkPayload) error {
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
	isNewStream := s.appendAudioChunk(payload.StreamID, payload.MimeType, chunk)

	if !payload.Final {
		if isNewStream {
			go s.runPartialTranscription(campaignID, senderID, payload.StreamID)
		}
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
	return sendToSender(s, senderID, msg)
}

// runPartialTranscription implements design doc §4's "live partial-
// transcription feedback shown to the speaking player" by re-running
// the exact same batch Provider.Transcribe call the final pass uses,
// every partialTranscriptionInterval, against whatever audio has
// arrived so far — not a genuinely different streaming-ASR backend
// contract. A MediaRecorder recording concatenated up to any chunk
// boundary received so far is itself a valid, decodable file (the
// client already relies on exactly this for the final concatenation in
// takeAudioStream), so this needs no new transcription backend,
// protocol field, or client change — only Master re-transcribing more
// often. Meant to be called via `go s.runPartialTranscription(...)`,
// started once per stream from handleAudioChunk's first chunk — like
// runSlowPass, it recovers its own panics rather than relying on the
// triggering connection's own recover.
//
// Real cost tradeoff, explicit rather than hidden: each round
// re-transcribes the WHOLE recording so far, not just the audio new
// since the last round — cost grows with recording length, and the
// preview updates in visible jumps every interval rather than smoothly
// word-by-word. Both are accepted as reasonable for push-to-talk's
// bounded, few-seconds-to-perhaps-thirty-seconds recordings; a longer
// or continuously-open microphone would need the genuinely different
// real streaming-ASR-backend design this deliberately isn't.
//
// Every partial send goes through sendPartialTranscriptionIfStillRecording
// rather than a bare sendToSender — see that function's own doc comment
// for the actual race it closes: a partial pass that started before the
// Final chunk arrived could otherwise finish (its own Transcribe call
// takes real time) and get delivered AFTER the true, complete
// is_final:true result, visibly reverting the player's input box to
// stale, truncated text.
func (s *Server) runPartialTranscription(campaignID, senderID, streamID string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("recovered from panic in partial transcription", "panic", r, "stream_id", streamID)
		}
	}()

	ticker := time.NewTicker(partialTranscriptionInterval)
	defer ticker.Stop()

	for range ticker.C {
		audio, mimeType, ok := s.peekAudioStream(streamID)
		if !ok {
			return // finalized (or removed) since the last tick — stop polling
		}
		if len(audio) == 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), partialTranscriptionTimeout)
		text, err := s.transcription.Transcribe(ctx, audio, mimeType)
		cancel()
		if err != nil {
			// A transient failure on one round isn't fatal to the
			// feature — the next tick retries with more audio, and the
			// final pass on release is entirely unaffected either way.
			s.logger.Warn("partial transcription round failed, will retry next interval", "error", err, "stream_id", streamID)
			continue
		}
		if text == "" {
			continue
		}

		msg, err := newMessage(campaignID, protocol.MessageTypeAudioTranscription, protocol.AudioTranscriptionPayload{
			StreamID: streamID,
			Text:     text,
			IsFinal:  false,
		})
		if err != nil {
			continue
		}
		if err := s.sendPartialTranscriptionIfStillRecording(streamID, senderID, msg); err != nil {
			s.logger.Warn("failed to send partial transcription", "error", err, "stream_id", streamID)
		}
	}
}

// appendAudioChunk records data as the next chunk of streamID's
// in-progress recording, creating its buffer on first use. isNewStream
// reports whether this call created that buffer — handleAudioChunk uses
// it to start exactly one runPartialTranscription goroutine per
// recording, on its first (non-Final) chunk.
func (s *Server) appendAudioChunk(streamID, mimeType string, data []byte) (isNewStream bool) {
	s.audioStreamsMu.Lock()
	defer s.audioStreamsMu.Unlock()
	buf, ok := s.audioStreams[streamID]
	if !ok {
		buf = &audioStreamBuffer{}
		s.audioStreams[streamID] = buf
		isNewStream = true
	}
	buf.mimeType = mimeType
	buf.chunks = append(buf.chunks, data)
	return isNewStream
}

// peekAudioStream returns a copy of streamID's currently-buffered audio
// and mime type WITHOUT removing it — unlike takeAudioStream (called
// once, on finalization), this is called repeatedly by
// runPartialTranscription while a recording is still in progress. ok is
// false once streamID has no buffer at all: never started, or already
// finalized (takeAudioStream deletes the entry) — the caller uses this
// to stop polling once the real, final transcription has taken over.
func (s *Server) peekAudioStream(streamID string) (audio []byte, mimeType string, ok bool) {
	s.audioStreamsMu.Lock()
	defer s.audioStreamsMu.Unlock()
	buf, exists := s.audioStreams[streamID]
	if !exists {
		return nil, "", false
	}
	var total int
	for _, c := range buf.chunks {
		total += len(c)
	}
	audio = make([]byte, 0, total)
	for _, c := range buf.chunks {
		audio = append(audio, c...)
	}
	return audio, buf.mimeType, true
}

// sendPartialTranscriptionIfStillRecording delivers msg (a partial
// audio.transcription) only if streamID's buffer still exists — checked
// under the same audioStreamsMu that takeAudioStream locks to delete it
// on finalization. This is the actual fix for a real race: without it,
// a partial pass that started before the Final chunk arrived could
// finish (its own Transcribe call takes real time) and get delivered
// AFTER the real, complete is_final:true result, visibly reverting the
// player's input box to stale, truncated text — the client
// (onAudioTranscription) simply overwrites the box with whatever
// arrives most recently, with no ordering/timestamp check of its own.
// Because this check and takeAudioStream's own delete share one lock,
// whichever of the two runs first is guaranteed to complete before the
// other starts: a partial that wins the race sends (still holding the
// lock) before the Final path's delete can proceed, and a partial that
// loses the race sees the entry already gone and sends nothing. A plain
// unlocked "check then send" would leave that race open. handleAudioChunk's
// own final-message send also goes through sendToSender rather than a
// direct connection write, so both messages funnel through the same
// per-connection outbox channel — FIFO delivery order then matches this
// lock's ordering guarantee instead of depending on whichever goroutine
// happens to reach the socket first.
func (s *Server) sendPartialTranscriptionIfStillRecording(streamID, senderID string, msg protocol.AudioTranscriptionMessage) error {
	s.audioStreamsMu.Lock()
	defer s.audioStreamsMu.Unlock()
	if _, exists := s.audioStreams[streamID]; !exists {
		return nil
	}
	return sendToSender(s, senderID, msg)
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
