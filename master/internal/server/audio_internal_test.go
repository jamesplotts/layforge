// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
)

// countingTranscriptionProvider is a minimal transcription.Provider for
// this package's own white-box tests — package server_test's
// fakeTranscriptionProvider isn't reachable from here (different Go
// package). text is returned verbatim; calls counts invocations,
// guarded by mu since runPartialTranscription calls this from its own
// background goroutine.
type countingTranscriptionProvider struct {
	text string

	mu    sync.Mutex
	calls int
}

func (p *countingTranscriptionProvider) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.text, nil
}

func (p *countingTranscriptionProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newAudioTestServer builds a bare *Server with just enough state for
// runPartialTranscription/appendAudioChunk/takeAudioStream/
// sendPartialTranscriptionIfStillRecording to run — these tests are
// about that specific machinery, not the full WS/HTTP stack
// audio_test.go's black-box tests already cover.
func newAudioTestServer(provider *countingTranscriptionProvider) *Server {
	return &Server{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		hub:           session.NewHub(),
		transcription: provider,
		audioStreams:  make(map[string]*audioStreamBuffer),
	}
}

// awaitPartial reads from client's outbox until an audio.transcription
// arrives (or the deadline passes), returning its payload. Used instead
// of a fixed sleep so these tests aren't flakier or slower than the
// shrunk interval actually requires.
func awaitPartial(t *testing.T, client *session.Client, deadline time.Duration) (protocol.AudioTranscriptionPayload, bool) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case payload := <-client.Outbox():
			var msg protocol.AudioTranscriptionMessage
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("unmarshaling outbox payload: %v", err)
			}
			return msg.Payload, true
		case <-timer.C:
			return protocol.AudioTranscriptionPayload{}, false
		}
	}
}

func TestRunPartialTranscription_PeriodicallyTranscribesGrowingBuffer(t *testing.T) {
	old := partialTranscriptionInterval
	partialTranscriptionInterval = 20 * time.Millisecond
	defer func() { partialTranscriptionInterval = old }()

	provider := &countingTranscriptionProvider{text: "so far, so good"}
	s := newAudioTestServer(provider)
	client := s.hub.Register("campaign-1", "player-a")
	defer s.hub.Unregister(client)

	s.appendAudioChunk("stream-1", "audio/webm", []byte("hello"))
	go s.runPartialTranscription("campaign-1", "player-a", "stream-1")

	payload, ok := awaitPartial(t, client, time.Second)
	if !ok {
		t.Fatal("no partial audio.transcription received within 1s")
	}
	if payload.StreamID != "stream-1" {
		t.Errorf("StreamID = %q, want stream-1", payload.StreamID)
	}
	if payload.Text != "so far, so good" {
		t.Errorf("Text = %q, want %q", payload.Text, "so far, so good")
	}
	if payload.IsFinal {
		t.Error("IsFinal = true, want false (this is a partial result)")
	}

	// A second round should follow — proving this genuinely repeats
	// rather than firing exactly once.
	if _, ok := awaitPartial(t, client, time.Second); !ok {
		t.Fatal("no second partial audio.transcription received within 1s")
	}
	if provider.callCount() < 2 {
		t.Errorf("Transcribe called %d times, want at least 2", provider.callCount())
	}

	s.takeAudioStream("stream-1") // finalize, so the goroutine stops
}

func TestRunPartialTranscription_StopsPollingOnceStreamFinalized(t *testing.T) {
	old := partialTranscriptionInterval
	partialTranscriptionInterval = 20 * time.Millisecond
	defer func() { partialTranscriptionInterval = old }()

	provider := &countingTranscriptionProvider{text: "partial text"}
	s := newAudioTestServer(provider)
	client := s.hub.Register("campaign-1", "player-a")
	defer s.hub.Unregister(client)

	s.appendAudioChunk("stream-1", "audio/webm", []byte("hello"))
	go s.runPartialTranscription("campaign-1", "player-a", "stream-1")

	if _, ok := awaitPartial(t, client, time.Second); !ok {
		t.Fatal("no partial audio.transcription received within 1s — precondition for this test failed")
	}

	// Finalize exactly like handleAudioChunk's own Final-chunk path does.
	s.takeAudioStream("stream-1")

	// No further partial should ever arrive — poll for a good multiple
	// of the (shrunk) interval and confirm the outbox stays quiet.
	if payload, ok := awaitPartial(t, client, 300*time.Millisecond); ok {
		t.Errorf("received a partial audio.transcription (%+v) after finalization, want none", payload)
	}
}

func TestSendPartialTranscriptionIfStillRecording_DropsMessageAfterFinalization(t *testing.T) {
	provider := &countingTranscriptionProvider{}
	s := newAudioTestServer(provider)
	client := s.hub.Register("campaign-1", "player-a")
	defer s.hub.Unregister(client)

	s.appendAudioChunk("stream-1", "audio/webm", []byte("hello"))
	s.takeAudioStream("stream-1") // finalize before the partial ever tries to send

	msg, err := newMessage("campaign-1", protocol.MessageTypeAudioTranscription, protocol.AudioTranscriptionPayload{
		StreamID: "stream-1",
		Text:     "stale partial — must never be delivered",
		IsFinal:  false,
	})
	if err != nil {
		t.Fatalf("newMessage() error = %v", err)
	}
	if err := s.sendPartialTranscriptionIfStillRecording("stream-1", "player-a", msg); err != nil {
		t.Fatalf("sendPartialTranscriptionIfStillRecording() error = %v", err)
	}

	select {
	case payload := <-client.Outbox():
		t.Fatalf("message was delivered despite the stream already being finalized: %s", payload)
	default:
		// Correct: nothing queued.
	}
}
