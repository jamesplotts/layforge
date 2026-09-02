// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package imagegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testWorkflow = `{
	"3": {"class_type": "KSampler", "inputs": {"seed": 1}},
	"6": {"class_type": "CLIPTextEncode", "inputs": {"text": "%%LAYFORGE_PROMPT%%"}},
	"9": {"class_type": "SaveImage", "inputs": {}}
}`

func TestNewComfyUIProvider_MissingPlaceholder_ReturnsError(t *testing.T) {
	_, err := NewComfyUIProvider("http://localhost:8188", `{"3": {"class_type": "KSampler"}}`)
	if err == nil {
		t.Fatal("NewComfyUIProvider() error = nil, want an error for a template with no placeholder")
	}
}

func TestNewComfyUIProvider_EmptyBaseURL_ReturnsError(t *testing.T) {
	_, err := NewComfyUIProvider("", testWorkflow)
	if err == nil {
		t.Fatal("NewComfyUIProvider() error = nil, want an error for an empty baseURL")
	}
}

// fakeComfyUI simulates just enough of ComfyUI's real REST API
// (/prompt, /history/{id}, /view) to test ComfyUIProvider's own HTTP
// client logic — request building, polling, and response parsing. It
// cannot verify these are the *actual* shapes a real ComfyUI instance
// returns (this session had no live instance to check against — see
// package doc comment); it only proves ComfyUIProvider correctly speaks
// the documented shape it was built against.
type fakeComfyUI struct {
	server *httptest.Server

	lastPromptBody map[string]any
	promptID       string
	// completedAfterPolls: /history returns "not found yet" this many
	// times before returning a completed entry — simulates real
	// generation taking a few seconds.
	completedAfterPolls int
	pollCount           int
	imageFilename       string
	noImageOutput       bool
}

func newFakeComfyUI(t *testing.T) *fakeComfyUI {
	t.Helper()
	f := &fakeComfyUI{promptID: "prompt-123", imageFilename: "scene_00001_.png"}
	mux := http.NewServeMux()
	mux.HandleFunc("/prompt", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.lastPromptBody = body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"prompt_id": f.promptID, "number": 1})
	})
	mux.HandleFunc("/history/", func(w http.ResponseWriter, r *http.Request) {
		f.pollCount++
		w.Header().Set("Content-Type", "application/json")
		if f.pollCount <= f.completedAfterPolls {
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		outputs := map[string]any{}
		if f.noImageOutput {
			outputs["9"] = map[string]any{"images": []any{}}
		} else {
			outputs["9"] = map[string]any{"images": []map[string]any{
				{"filename": f.imageFilename, "subfolder": "", "type": "output"},
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			f.promptID: map[string]any{
				"outputs": outputs,
				"status":  map[string]any{"completed": true, "status_str": "success"},
			},
		})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func TestComfyUIProvider_GenerateSceneImage_SubmitsPromptAndReturnsViewURL(t *testing.T) {
	fake := newFakeComfyUI(t)
	p, err := NewComfyUIProvider(fake.server.URL, testWorkflow)
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}
	// Make polling fast for the test.
	p.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	imageURL, err := p.GenerateSceneImage(ctx, "a moonlit forest clearing", "")
	if err != nil {
		t.Fatalf("GenerateSceneImage() error = %v", err)
	}

	if !strings.Contains(imageURL, "filename="+fake.imageFilename) {
		t.Errorf("imageURL = %q, want it to reference %q", imageURL, fake.imageFilename)
	}
	if !strings.HasPrefix(imageURL, fake.server.URL+"/view?") {
		t.Errorf("imageURL = %q, want it to start with %s/view?", imageURL, fake.server.URL)
	}

	// The prompt text must have actually replaced the placeholder in the
	// workflow submitted to ComfyUI — not the literal placeholder token.
	promptGraph, ok := fake.lastPromptBody["prompt"].(map[string]any)
	if !ok {
		t.Fatalf("submitted body's \"prompt\" field = %v, want a workflow graph object", fake.lastPromptBody["prompt"])
	}
	node6, _ := promptGraph["6"].(map[string]any)
	inputs, _ := node6["inputs"].(map[string]any)
	text, _ := inputs["text"].(string)
	if text != "a moonlit forest clearing" {
		t.Errorf("submitted node 6 text = %q, want the real prompt substituted in place of the placeholder", text)
	}
}

func TestComfyUIProvider_GenerateSceneImage_MaturityTierAppendedToPrompt(t *testing.T) {
	fake := newFakeComfyUI(t)
	p, err := NewComfyUIProvider(fake.server.URL, testWorkflow)
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}
	p.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := p.GenerateSceneImage(ctx, "a tavern brawl", "no graphic gore"); err != nil {
		t.Fatalf("GenerateSceneImage() error = %v", err)
	}

	promptGraph := fake.lastPromptBody["prompt"].(map[string]any)
	node6 := promptGraph["6"].(map[string]any)
	inputs := node6["inputs"].(map[string]any)
	text := inputs["text"].(string)
	if !strings.Contains(text, "a tavern brawl") || !strings.Contains(text, "no graphic gore") {
		t.Errorf("submitted prompt text = %q, want both the scene prompt and the maturity tier guidance", text)
	}
}

func TestComfyUIProvider_GenerateSceneImage_WaitsAcrossMultiplePolls(t *testing.T) {
	fake := newFakeComfyUI(t)
	fake.completedAfterPolls = 2 // not ready for the first 2 polls
	p, err := NewComfyUIProvider(fake.server.URL, testWorkflow)
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}
	p.pollInterval = 5 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	imageURL, err := p.GenerateSceneImage(ctx, "a quiet library", "")
	if err != nil {
		t.Fatalf("GenerateSceneImage() error = %v", err)
	}
	if !strings.Contains(imageURL, fake.imageFilename) {
		t.Errorf("imageURL = %q, want it to eventually reference the completed image", imageURL)
	}
	if fake.pollCount < 3 {
		t.Errorf("pollCount = %d, want at least 3 (2 not-ready + 1 completed)", fake.pollCount)
	}
}

func TestComfyUIProvider_GenerateSceneImage_NoImageOutput_ReturnsClearError(t *testing.T) {
	fake := newFakeComfyUI(t)
	fake.noImageOutput = true
	p, err := NewComfyUIProvider(fake.server.URL, testWorkflow)
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}
	p.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = p.GenerateSceneImage(ctx, "an empty room", "")
	if err == nil {
		t.Fatal("GenerateSceneImage() error = nil, want an error when the completed job produced no image output")
	}
}

func TestComfyUIProvider_GenerateSceneImage_ContextTimeout_ReturnsError(t *testing.T) {
	fake := newFakeComfyUI(t)
	fake.completedAfterPolls = 1000 // never completes within the test
	p, err := NewComfyUIProvider(fake.server.URL, testWorkflow)
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}
	p.pollInterval = 5 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err = p.GenerateSceneImage(ctx, "a scene that never renders", "")
	if err == nil {
		t.Fatal("GenerateSceneImage() error = nil, want a context-deadline error")
	}
}

func TestComfyUIProvider_GenerateSceneImage_ServerUnreachable_ReturnsClearError(t *testing.T) {
	p, err := NewComfyUIProvider("http://127.0.0.1:1", testWorkflow) // nothing listens on port 1
	if err != nil {
		t.Fatalf("NewComfyUIProvider() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = p.GenerateSceneImage(ctx, "unreachable", "")
	if err == nil {
		t.Fatal("GenerateSceneImage() error = nil, want an error when ComfyUI is unreachable")
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("error = %q, want it to hint that ComfyUI might not be running", err)
	}
}

func TestJSONEscapeForTemplate_EscapesQuotesAndNewlines(t *testing.T) {
	got := jsonEscapeForTemplate(`a "quoted" scene` + "\nwith a newline")
	want := `a \"quoted\" scene\nwith a newline`
	if got != want {
		t.Errorf("jsonEscapeForTemplate() = %q, want %q", got, want)
	}
	// The escaped result must be usable as the *contents* of a JSON
	// string when re-wrapped in quotes.
	var roundTripped string
	if err := json.Unmarshal([]byte(fmt.Sprintf(`"%s"`, got)), &roundTripped); err != nil {
		t.Fatalf("re-parsing escaped text as JSON: %v", err)
	}
}
