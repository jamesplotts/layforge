// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/llm"
)

// fakeLLMProvider is a minimal llm.Provider fake local to this test
// file — internal/server's own fakeLLMProvider isn't importable from
// here (unexported, different package), so this mirrors its shape at
// the scale this package's own tests need.
type fakeLLMProvider struct {
	response llm.CompletionResponse
	err      error
	lastReq  llm.CompletionRequest
}

func (f *fakeLLMProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return llm.CompletionResponse{}, f.err
	}
	return f.response, nil
}

func writePackToolCall(t *testing.T, files []campaignpack.GeneratedFile) llm.CompletionResponse {
	t.Helper()
	type wireFile struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	wireFiles := make([]wireFile, len(files))
	for i, f := range files {
		wireFiles[i] = wireFile{Path: f.Path, Content: f.Content}
	}
	args, err := json.Marshal(map[string]any{"files": wireFiles})
	if err != nil {
		t.Fatalf("marshaling tool call args: %v", err)
	}
	return llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "write_campaign_pack", Arguments: args},
		},
	}
}

func validCampaignMD() string {
	return "---\nid: haunted-lighthouse\ntitle: The Drowned Light\nlevel_range: \"1-3\"\npvp_policy: pve_only\n---\nA storm-battered lighthouse hides a decades-old secret.\n"
}

func TestGenerate_SendsExactlyOneStructuredTool(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
	})}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse on a storm-battered coast.",
		MinLevel:    1,
		MaxLevel:    3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if len(provider.lastReq.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(provider.lastReq.Tools))
	}
	tool := provider.lastReq.Tools[0]
	if tool.Name != "write_campaign_pack" {
		t.Errorf("tool.Name = %q, want write_campaign_pack", tool.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		t.Fatalf("tool.Parameters is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool.Parameters has no properties object: %v", schema)
	}
	if _, ok := props["files"]; !ok {
		t.Errorf("tool.Parameters.properties has no \"files\" key: %v", props)
	}
}

func TestGenerate_ReturnsFilesFromToolCall(t *testing.T) {
	want := []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
		{Path: "locations/lighthouse-base.md", Content: "---\nid: lighthouse-base\n---\nThe base of the lighthouse.\n"},
	}
	provider := &fakeLLMProvider{response: writePackToolCall(t, want)}

	got, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(files) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("files[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestGenerate_NoToolCall_ReturnsError(t *testing.T) {
	provider := &fakeLLMProvider{response: llm.CompletionResponse{Text: "Sure, here's an adventure..."}}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want an error (model didn't call the tool)")
	}
}

func TestGenerate_NoFiles_ReturnsError(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, nil)}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want an error (empty files)")
	}
}

func TestGenerate_MissingCampaignMD_ReturnsError(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "locations/somewhere.md", Content: "---\nid: somewhere\n---\nSomewhere.\n"},
	})}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want an error (no campaign.md in the generated files)")
	}
}

// TestGenerate_FilesFieldStringEncoded_StillParses covers a real quirk
// confirmed live against a local Ollama model: it called the tool
// correctly but stringified the "files" array (a JSON-encoded string
// containing the array, rather than a genuine nested array) even though
// the offered schema said array.
func TestGenerate_FilesFieldStringEncoded_StillParses(t *testing.T) {
	filesJSON, err := json.Marshal([]campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
	})
	if err != nil {
		t.Fatalf("marshaling files JSON: %v", err)
	}
	args, err := json.Marshal(map[string]any{"files": string(filesJSON)})
	if err != nil {
		t.Fatalf("marshaling tool call args: %v", err)
	}
	provider := &fakeLLMProvider{response: llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "write_campaign_pack", Arguments: args}},
	}}

	got, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got) != 1 || got[0].Path != "campaign.md" {
		t.Errorf("files = %+v, want a single campaign.md entry", got)
	}
}

// TestGenerate_StringEncodedFilesWithInvalidEscape_StillParses covers
// another real quirk confirmed live, repeatedly, against a real local
// model: when it stringifies the "files" array (see the test above),
// the stringified JSON itself sometimes contains a backslash that
// isn't a valid JSON escape (e.g. "\i") — a well-documented class of
// mistake models make hand-writing nested JSON. Generate must recover
// by treating that stray backslash as literal, not fail outright.
func TestGenerate_StringEncodedFilesWithInvalidEscape_StillParses(t *testing.T) {
	// Deliberately hand-built rather than round-tripped through
	// json.Marshal, since the whole point is content Go's own encoder
	// would never itself produce — an invalid \i escape inside the
	// stringified inner JSON.
	innerJSON := `[{"path":"campaign.md","content":"---\nid: haunted-lighthouse\n---\nThe keeper\involves a storm.\n"}]`
	args, err := json.Marshal(map[string]string{"files": innerJSON})
	if err != nil {
		t.Fatalf("marshaling tool call args: %v", err)
	}
	provider := &fakeLLMProvider{response: llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "write_campaign_pack", Arguments: args}},
	}}

	got, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v (want the invalid \\i escape to be repaired, not fatal)", err)
	}
	if len(got) != 1 || got[0].Path != "campaign.md" {
		t.Fatalf("files = %+v, want a single campaign.md entry", got)
	}
	if !strings.Contains(got[0].Content, `keeper\involves`) {
		t.Errorf("Content = %q, want the literal backslash preserved (keeper\\involves)", got[0].Content)
	}
}

func TestGenerate_ProviderError_Propagates(t *testing.T) {
	provider := &fakeLLMProvider{err: errors.New("boom")}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want the provider's error to propagate")
	}
}

// TestGenerate_WellFormedResult_ActuallyLoadsViaTheRealParser is the
// end-to-end proof this plan calls for: a generation whose shape a real
// LLM could plausibly produce must genuinely satisfy campaignpack.LoadPack
// — the real parser, not a second hand-rolled validation path.
func TestGenerate_WellFormedResult_ActuallyLoadsViaTheRealParser(t *testing.T) {
	files := []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
		{Path: "locations/lighthouse-base.md", Content: "---\nid: lighthouse-base\nconnections: [lighthouse-lamp-room]\n---\nThe damp stone base of the lighthouse.\n"},
		{Path: "locations/lighthouse-lamp-room.md", Content: "---\nid: lighthouse-lamp-room\nconnections: [lighthouse-base]\n---\nThe lamp room at the top.\n"},
		{Path: "npcs/keeper-mara.md", Content: "---\nid: keeper-mara\nlocation: lighthouse-base\n---\nThe last keeper, or what's left of her.\n"},
		{Path: "encounters/the-drowned-thing.md", Content: "---\nid: the-drowned-thing\nlocation: lighthouse-lamp-room\ninvolves: [keeper-mara]\n---\nSomething rises from the tide pool below.\n"},
	}
	provider := &fakeLLMProvider{response: writePackToolCall(t, files)}

	got, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse on a storm-battered coast.", MinLevel: 1, MaxLevel: 3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	dir := t.TempDir()
	for _, f := range got {
		full := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	pack, err := campaignpack.LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack() error = %v (generated pack does not parse)", err)
	}
	if pack.ID != "haunted-lighthouse" {
		t.Errorf("pack.ID = %q, want haunted-lighthouse", pack.ID)
	}
	if len(pack.Locations) != 2 || len(pack.NPCs) != 1 || len(pack.Encounters) != 1 {
		t.Errorf("pack = %+v, want 2 locations, 1 npc, 1 encounter", pack)
	}
}
