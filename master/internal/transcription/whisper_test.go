// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package transcription_test

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/transcription"
)

func TestWhisperProvider_Transcribe_Success_PostsMultipartAndReturnsText(t *testing.T) {
	var gotMethod, gotPath, gotModel, gotFilename, gotFileContentType string
	var gotFileBytes []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		gotModel = r.FormValue("model")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile(file) error = %v", err)
		}
		defer file.Close()
		gotFilename = header.Filename
		gotFileContentType = header.Header.Get("Content-Type")
		gotFileBytes, err = io.ReadAll(file)
		if err != nil {
			t.Fatalf("reading uploaded file error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"the guard is lying to us"}`))
	}))
	defer ts.Close()

	p := transcription.NewWhisperProvider(ts.URL, "base")
	text, err := p.Transcribe(context.Background(), []byte("fake-audio-bytes"), "audio/webm;codecs=opus")
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if text != "the guard is lying to us" {
		t.Errorf("Transcribe() text = %q, want %q", text, "the guard is lying to us")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("request method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("request path = %q, want /v1/audio/transcriptions", gotPath)
	}
	if gotModel != "base" {
		t.Errorf("form model = %q, want %q", gotModel, "base")
	}
	if string(gotFileBytes) != "fake-audio-bytes" {
		t.Errorf("uploaded file bytes = %q, want %q", string(gotFileBytes), "fake-audio-bytes")
	}
	if !strings.HasSuffix(gotFilename, ".webm") {
		t.Errorf("uploaded filename = %q, want a .webm suffix (derived from mimeType)", gotFilename)
	}
	gotMediaType, _, err := mime.ParseMediaType(gotFileContentType)
	if err != nil {
		t.Fatalf("parsing uploaded file Content-Type %q: %v", gotFileContentType, err)
	}
	if gotMediaType != "audio/webm" {
		t.Errorf("uploaded file Content-Type = %q, want audio/webm(;codecs=opus)", gotFileContentType)
	}
}

func TestWhisperProvider_Transcribe_NonOKStatus_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not loaded"))
	}))
	defer ts.Close()

	p := transcription.NewWhisperProvider(ts.URL, "base")
	_, err := p.Transcribe(context.Background(), []byte("audio"), "audio/wav")
	if err == nil {
		t.Fatal("Transcribe() error = nil, want an error for a non-200 response")
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Errorf("Transcribe() error = %v, want it to include the response body", err)
	}
}

func TestWhisperProvider_Transcribe_MalformedJSONResponse_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	p := transcription.NewWhisperProvider(ts.URL, "base")
	_, err := p.Transcribe(context.Background(), []byte("audio"), "audio/wav")
	if err == nil {
		t.Fatal("Transcribe() error = nil, want an error for a malformed response body")
	}
}

func TestWhisperProvider_Transcribe_UnreachableServer_ReturnsError(t *testing.T) {
	p := transcription.NewWhisperProvider("http://127.0.0.1:1", "base")
	_, err := p.Transcribe(context.Background(), []byte("audio"), "audio/wav")
	if err == nil {
		t.Fatal("Transcribe() error = nil, want an error when the server is unreachable")
	}
}

func TestNewWhisperProvider_ImplementsProvider(t *testing.T) {
	var _ transcription.Provider = transcription.NewWhisperProvider("http://localhost:9000", "base")
}
