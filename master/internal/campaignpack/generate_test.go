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

// TestGenerate_SystemPromptScopesToOutlinePlusFirstChapterOnly is the
// regression test for the actual repetition fix (live-testing "The
// Sacrifice" found a single-shot whole-pack generation degenerates into
// repetitive prose in its last file) — Generate's system prompt must
// instruct the model to produce the chapters outline but fully author
// only the first chapter's content, not the whole campaign in one call.
func TestGenerate_SystemPromptScopesToOutlinePlusFirstChapterOnly(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
	})}

	_, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A haunted lighthouse.", MinLevel: 1, MaxLevel: 3,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	prompt := provider.lastReq.SystemPrompt
	if !strings.Contains(prompt, "chapters") {
		t.Errorf("SystemPrompt does not mention chapters: %s", prompt)
	}
	if !strings.Contains(prompt, "first chapter") {
		t.Errorf("SystemPrompt does not scope generation to the first chapter only: %s", prompt)
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

// --- GenerateChapter ---

// existingPackWithChapters builds a real, valid on-disk pack (two
// chapters, one already-authored location/npc in chapter-1) for
// GenerateChapter/GenerateSideQuest tests that need genuine existing-pack
// context to read via LoadPack, not a hand-built Pack value.
func existingPackWithChapters(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "campaign.md"), `---
id: the-sunken-vault
title: The Sunken Vault
level_range: "1-6"
chapters:
  - id: chapter-1
    title: "The Flooded Gate"
    level_range: "1-2"
    summary: "The party finds the vault's entrance underwater."
  - id: chapter-2
    title: "The Drowned Halls"
    level_range: "2-4"
    summary: "The party explores the vault's flooded interior."
---
An ancient vault, sealed beneath a lake, is finally surfacing.
`)
	if err := os.Mkdir(filepath.Join(dir, "locations"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "locations", "lake-shore.md"), "---\nid: lake-shore\nchapter: chapter-1\n---\nThe muddy shore of the lake.\n")
	if err := os.Mkdir(filepath.Join(dir, "npcs"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "npcs", "diver-nessa.md"), "---\nid: diver-nessa\nlocation: lake-shore\nchapter: chapter-1\n---\nA local diver who knows the lake.\n")
	return dir
}

func TestGenerateChapter_SendsExistingPremiseAndChapterContext(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "locations/flooded-tunnel.md", Content: "---\nid: flooded-tunnel\nchapter: chapter-2\n---\nA tunnel, half full of black water.\n"},
	})}

	_, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: dir,
		ChapterID:       "chapter-2",
	})
	if err != nil {
		t.Fatalf("GenerateChapter() error = %v", err)
	}

	prompt := provider.lastReq.UserPrompt
	for _, want := range []string{"The Sunken Vault", "The Drowned Halls", "chapter-2", "lake-shore", "diver-nessa"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("UserPrompt = %q, want it to contain %q", prompt, want)
		}
	}
}

func TestGenerateChapter_UnknownChapterID_ReturnsError(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "locations/somewhere.md", Content: "---\nid: somewhere\n---\nSomewhere.\n"},
	})}

	_, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: dir,
		ChapterID:       "chapter-does-not-exist",
	})
	if err == nil {
		t.Fatal("GenerateChapter() error = nil, want an error for an unknown chapter id")
	}
}

func TestGenerateChapter_NonexistentPackDir_ReturnsError(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, nil)}

	_, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: "/nonexistent/pack/dir",
		ChapterID:       "chapter-1",
	})
	if err == nil {
		t.Fatal("GenerateChapter() error = nil, want an error for a pack dir that doesn't load")
	}
}

func TestGenerateChapter_ReturnsFilesFromToolCall(t *testing.T) {
	dir := existingPackWithChapters(t)
	want := []campaignpack.GeneratedFile{
		{Path: "locations/flooded-tunnel.md", Content: "---\nid: flooded-tunnel\nchapter: chapter-2\n---\nA tunnel.\n"},
		{Path: "encounters/the-guardian.md", Content: "---\nid: the-guardian\nchapter: chapter-2\n---\nSomething guards the tunnel.\n"},
	}
	provider := &fakeLLMProvider{response: writePackToolCall(t, want)}

	got, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: dir,
		ChapterID:       "chapter-2",
	})
	if err != nil {
		t.Fatalf("GenerateChapter() error = %v", err)
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

func TestGenerateChapter_ResponseIncludesCampaignMD_ReturnsError(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
	})}

	_, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: dir,
		ChapterID:       "chapter-2",
	})
	if err == nil {
		t.Fatal("GenerateChapter() error = nil, want an error when the model re-includes campaign.md")
	}
}

func TestGenerateChapter_NoFiles_ReturnsError(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, nil)}

	_, err := campaignpack.GenerateChapter(context.Background(), provider, "test-model", campaignpack.GenerateChapterRequest{
		ExistingPackDir: dir,
		ChapterID:       "chapter-2",
	})
	if err == nil {
		t.Fatal("GenerateChapter() error = nil, want an error (empty files)")
	}
}

// --- GenerateSideQuest ---

func TestGenerateSideQuest_SendsPremiseDescriptionAndPlayerCount(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "encounters/lost-cart.md", Content: "---\nid: lost-cart\nside_quest: sq-lost-cart\nmin_players: 1\nmax_players: 3\n---\nA merchant's cart is stuck in the mud.\n"},
	})}

	_, err := campaignpack.GenerateSideQuest(context.Background(), provider, "test-model", campaignpack.GenerateSideQuestRequest{
		ExistingPackDir: dir,
		Description:     "A quick roadside distraction for a short-handed party.",
		MaxPlayers:      3,
	})
	if err != nil {
		t.Fatalf("GenerateSideQuest() error = %v", err)
	}

	prompt := provider.lastReq.UserPrompt
	for _, want := range []string{"The Sunken Vault", "A quick roadside distraction for a short-handed party.", "3"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("UserPrompt = %q, want it to contain %q", prompt, want)
		}
	}
}

func TestGenerateSideQuest_ReturnsFilesFromToolCall(t *testing.T) {
	dir := existingPackWithChapters(t)
	want := []campaignpack.GeneratedFile{
		{Path: "encounters/lost-cart.md", Content: "---\nid: lost-cart\nside_quest: sq-lost-cart\nmin_players: 1\nmax_players: 3\n---\nA merchant's cart is stuck in the mud.\n"},
	}
	provider := &fakeLLMProvider{response: writePackToolCall(t, want)}

	got, err := campaignpack.GenerateSideQuest(context.Background(), provider, "test-model", campaignpack.GenerateSideQuestRequest{
		ExistingPackDir: dir,
		Description:     "A quick roadside distraction.",
		MaxPlayers:      3,
	})
	if err != nil {
		t.Fatalf("GenerateSideQuest() error = %v", err)
	}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("files = %+v, want %+v", got, want)
	}
}

func TestGenerateSideQuest_ResponseIncludesCampaignMD_ReturnsError(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: validCampaignMD()},
	})}

	_, err := campaignpack.GenerateSideQuest(context.Background(), provider, "test-model", campaignpack.GenerateSideQuestRequest{
		ExistingPackDir: dir,
		Description:     "A quick roadside distraction.",
		MaxPlayers:      3,
	})
	if err == nil {
		t.Fatal("GenerateSideQuest() error = nil, want an error when the model re-includes campaign.md")
	}
}

func TestGenerateSideQuest_NoFiles_ReturnsError(t *testing.T) {
	dir := existingPackWithChapters(t)
	provider := &fakeLLMProvider{response: writePackToolCall(t, nil)}

	_, err := campaignpack.GenerateSideQuest(context.Background(), provider, "test-model", campaignpack.GenerateSideQuestRequest{
		ExistingPackDir: dir,
		Description:     "A quick roadside distraction.",
		MaxPlayers:      3,
	})
	if err == nil {
		t.Fatal("GenerateSideQuest() error = nil, want an error (empty files)")
	}
}

func TestGenerateSideQuest_NonexistentPackDir_ReturnsError(t *testing.T) {
	provider := &fakeLLMProvider{response: writePackToolCall(t, nil)}

	_, err := campaignpack.GenerateSideQuest(context.Background(), provider, "test-model", campaignpack.GenerateSideQuestRequest{
		ExistingPackDir: "/nonexistent/pack/dir",
		Description:     "A quick roadside distraction.",
		MaxPlayers:      3,
	})
	if err == nil {
		t.Fatal("GenerateSideQuest() error = nil, want an error for a pack dir that doesn't load")
	}
}

// TestGenerate_RepairsUnquotedColonInFrontMatterScalars covers a real,
// live-observed model quirk, confirmed twice independently (an npc's
// voice field, and a campaign.md chapters[].summary field): the model
// writes a front-matter scalar value containing an unescaped ": "
// sequence — e.g. `voice: precise, like a scalpel: not to wound` —
// which breaks YAML parsing because the embedded colon reads as the
// start of a nested mapping. Generate must repair this (by quoting the
// value) rather than surface a validation failure for a mistake this
// predictable. The scripted response below exercises every case the
// repair needs to get right in one pass: an unquoted top-level scalar
// with an embedded colon, the same bug nested under a chapters list
// item, an already-quoted value that must be left alone, and a colon in
// the markdown body that must never be touched (bodies aren't YAML).
func TestGenerate_RepairsUnquotedColonInFrontMatterScalars(t *testing.T) {
	campaignMD := `---
id: the-sacrifice
title: The Price of Dawn
chapters:
  - id: the-quest
    title: The Quest
    summary: Warden Garrick reveals the full truth: the cost is not optional
already_quoted: "kept: as-is"
---
Body text with a colon: this must survive untouched.
`
	npcMD := `---
id: warden-garrick
voice: precise, like a scalpel: not to wound but to find the line
---
Garrick speaks slowly.
`
	provider := &fakeLLMProvider{response: writePackToolCall(t, []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: campaignMD},
		{Path: "npcs/warden-garrick.md", Content: npcMD},
	})}

	got, err := campaignpack.Generate(context.Background(), provider, "test-model", campaignpack.GenerateRequest{
		Description: "A test.", MinLevel: 1, MaxLevel: 3,
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
		t.Fatalf("LoadPack() error = %v (repaired pack should parse via the real loader)", err)
	}
	if !strings.Contains(pack.Overview, "Body text with a colon: this must survive untouched.") {
		t.Errorf("Overview = %q, want the body's own colon left untouched", pack.Overview)
	}
	if len(pack.Chapters) != 1 || !strings.Contains(pack.Chapters[0].Summary, "the full truth: the cost is not optional") {
		t.Errorf("Chapters = %+v, want the nested summary's embedded colon preserved after repair", pack.Chapters)
	}
	if len(pack.NPCs) != 1 || !strings.Contains(pack.NPCs[0].Voice, "like a scalpel: not to wound") {
		t.Errorf("NPCs = %+v, want the voice field's embedded colon preserved after repair", pack.NPCs)
	}
}
