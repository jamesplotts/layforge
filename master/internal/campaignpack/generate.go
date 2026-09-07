// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jamesplotts/layforge/master/internal/llm"
)

// GenerateRequest is a Host's freeform adventure description plus the
// level range it should target (internal/admin's new "Generate a
// campaign pack with AI" flow, Campaign tab).
type GenerateRequest struct {
	Description string
	MinLevel    int
	MaxLevel    int
}

// GeneratedFile is one file the LLM produced, relative to the pack's
// own root (e.g. "campaign.md", "locations/lighthouse-base.md").
type GeneratedFile struct {
	Path    string
	Content string
}

// writePackToolName is the one tool Generate offers the model — never
// reachable from the DM tool-use loop (internal/server/dm_tools.go),
// this is a single-purpose completion call, the same "not part of the
// slow-pass dispatch switch" shape reviewCharacterTool
// (character_review.go) already uses for its own single-purpose verdict
// call.
const writePackToolName = "write_campaign_pack"

func writePackTool() llm.Tool {
	return llm.Tool{
		Name:        writePackToolName,
		Description: "Write the complete campaign pack you've designed. Call this exactly once with every file.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"required": ["files"],
			"properties": {
				"files": {
					"type": "array",
					"description": "Every file in the pack, including campaign.md.",
					"items": {
						"type": "object",
						"required": ["path", "content"],
						"properties": {
							"path": {"type": "string", "description": "Relative path, e.g. \"campaign.md\", \"locations/lighthouse-base.md\", \"npcs/keeper-mara.md\", \"encounters/the-drowned-thing.md\"."},
							"content": {"type": "string", "description": "The full file content: a YAML front matter block between --- lines, then the markdown body."}
						}
					}
				}
			}
		}`),
	}
}

// campaignGenerationSystemPrompt instructs the model on the exact file/
// front-matter shape internal/campaignpack.LoadPack actually requires
// (see loader.go — id is the only hard requirement on every file, but
// this asks for the fuller shape campaign-packs/sable-ravine/ itself
// uses, since a pack with only ids and no other fields would load but
// play badly) and restates CLAUDE.md's own legal rule inline: this is
// the one place a self-hoster's *generated content itself* could raise
// real legal exposure (design doc §6.4, §12), so it isn't left to the
// model's general good behavior the way the DM's own live narration
// prompts can be.
const campaignGenerationSystemPrompt = `You are designing a short, original tabletop RPG campaign pack from a Host's description.

Legal requirement, non-negotiable: everything you write must be original, SRD-legal content only. Never use proprietary Dungeons & Dragons terms, named published characters, or non-SRD monster names; never copy a published module's plot, named locations, or specific text. Tone-inspired is fine — direct reuse is not.

Call write_campaign_pack exactly once. This campaign is organized into chapters, each roughly one level's worth of content (about the amount of play it takes a party to earn a level — a useful size, not a strict rule). Produce:
- campaign.md: YAML front matter with id (a short lowercase-hyphenated slug), title, level_range (e.g. "1-3"), pvp_policy (one of pve_only, pvp_allowed — default pve_only unless the description implies otherwise), maturity_tier ("standard" unless the description clearly implies otherwise), image_maturity_tier ("family_friendly" unless the description clearly implies otherwise), shared_knowledge ("strict" unless the description implies otherwise), tone (a short list of adjectives), author ("AI-generated"), and chapters — a list of 2-5 entries (id, title, level_range, summary) sketching the whole campaign's arc across the requested level range. Body: a few paragraphs of overview prose plus a "## Hooks" section.
- Then fully author ONLY the first chapter's content (chapters[0]) — later chapters are generated separately, once the party is actually approaching them, so do not write their content now:
  - locations/*.md (3-5 files): YAML front matter with id, connections (a list of other location ids directly reachable from this one — build a real, traversable graph), chapter set to chapters[0]'s id. Body: a few paragraphs of prose description.
  - npcs/*.md (2-4 files): YAML front matter with id, location (which location id they're normally found at), stat_block_ref (a plain SRD creature/class type, e.g. "SRD Veteran" or "SRD Commoner" — never a named published monster), voice (a short phrase describing how they speak), chapter set to chapters[0]'s id. Body: a paragraph of personality/motivation.
  - encounters/*.md (2-3 files): YAML front matter with id, location, involves (a list of the npc ids present), chapter set to chapters[0]'s id. Body: a paragraph describing the situation and how it might unfold.

Every id across every file must be unique and lowercase-hyphenated. Keep the first chapter small and focused — enough for one real short session, not a sprawling setting.`

// chapterGenerationSystemPrompt instructs the model to extend an
// already-existing campaign pack with exactly one more chapter's worth
// of content — the targeted, smaller generation GenerateChapter uses
// once a Host's party is actually approaching a later chapter, rather
// than authoring the whole campaign's chapters up front in one long
// generation (see campaignGenerationSystemPrompt's own doc comment on
// why that's the real fix for a real repetition failure observed live).
const chapterGenerationSystemPrompt = `You are extending an existing tabletop RPG campaign pack with one chapter's worth of new content.

Legal requirement, non-negotiable: everything you write must be original, SRD-legal content only. Never use proprietary Dungeons & Dragons terms, named published characters, or non-SRD monster names; never copy a published module's plot, named locations, or specific text. Tone-inspired is fine — direct reuse is not.

Call write_campaign_pack exactly once with every new file for this chapter only. Do not include campaign.md — that file already exists and is not being changed. Produce:
- locations/*.md (3-5 new files): YAML front matter with id, connections (may reference already-established location ids too, to keep the whole map connected), chapter set to this chapter's id. Body: a few paragraphs of prose description.
- npcs/*.md (2-4 new files): YAML front matter with id, location, stat_block_ref (a plain SRD creature/class type — never a named published monster), voice, chapter set to this chapter's id. Body: a paragraph of personality/motivation.
- encounters/*.md (2-3 new files): YAML front matter with id, location, involves, chapter set to this chapter's id. Body: a paragraph describing the situation and how it might unfold.

Every new id must be unique — never reuse an id already established in this campaign — and lowercase-hyphenated. Stay consistent with the campaign's established premise and roster: reference existing NPCs/locations where it makes sense, and don't contradict or re-author anything already established.`

// sideQuestGenerationSystemPrompt instructs the model to write a short,
// self-contained side adventure for an existing campaign — the concrete
// need this whole feature was built for: a Host whose table is short a
// player some session, who wants something the remaining players can
// run without disrupting or spoiling the ongoing campaign's real
// chapters.
const sideQuestGenerationSystemPrompt = `You are writing a short, self-contained side adventure for an existing tabletop RPG campaign — the kind a Host pulls up when the full table isn't available (a player is out, or the group wants a lighter session) without disrupting the main campaign's ongoing chapters.

Legal requirement, non-negotiable: everything you write must be original, SRD-legal content only. Never use proprietary Dungeons & Dragons terms, named published characters, or non-SRD monster names; never copy a published module's plot, named locations, or specific text. Tone-inspired is fine — direct reuse is not.

Call write_campaign_pack exactly once with every file for this side quest. Do not include campaign.md. Produce a small, self-contained set (1-2 encounters, sized to the requested player count):
- locations/*.md (0-2 new files, only if genuinely needed — reusing an existing location is fine and often better): YAML front matter with id, connections, side_quest set to a new short id for this side quest. Body: a few paragraphs of prose description.
- npcs/*.md (0-2 new files): YAML front matter with id, location, stat_block_ref (a plain SRD creature/class type — never a named published monster), voice, side_quest set to the same side quest id. Body: a paragraph of personality/motivation.
- encounters/*.md (1-2 new files): YAML front matter with id, location, involves, side_quest set to the same side quest id, min_players and max_players sized to what was requested. Body: a paragraph describing the situation and how it might unfold.

This must stand entirely on its own: it must not require the full party, must not advance or contradict the main campaign's chapters, and must not assume anything happened that the Host didn't tell you about. Every new id must be unique and lowercase-hyphenated.`

// Generate calls provider once with a single structured tool call and
// returns the files it produced. It does not write anything to disk —
// see WriteAndValidate for that. A structured tool call (rather than
// hoping a plain completion emits well-formed multi-file markdown with
// correct front-matter delimiters) mirrors the same "structured JSON
// arguments via a tool call" pattern internal/server/dm_tools.go
// already relies on for reliability.
func Generate(ctx context.Context, provider llm.Provider, model string, req GenerateRequest) ([]GeneratedFile, error) {
	userPrompt := fmt.Sprintf("Adventure description: %s\nLevel range: %d-%d", req.Description, req.MinLevel, req.MaxLevel)

	files, err := generateFiles(ctx, provider, model, campaignGenerationSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("campaignpack: model generated an empty pack (no files)")
	}
	hasCampaignMD := false
	for _, f := range files {
		if f.Path == "campaign.md" {
			hasCampaignMD = true
			break
		}
	}
	if !hasCampaignMD {
		return nil, fmt.Errorf("campaignpack: generated pack has no campaign.md")
	}

	return files, nil
}

// GenerateChapterRequest targets one already-declared chapter (from an
// existing pack's campaign.md chapters outline) for content generation
// — the Host action for "the party is getting close to chapter 2, write
// it now" rather than authoring every chapter up front.
type GenerateChapterRequest struct {
	ExistingPackDir string
	ChapterID       string
}

// GenerateChapter loads the existing pack at req.ExistingPackDir (for
// its premise and already-established location/NPC roster, so new
// content stays consistent with it) and generates just req.ChapterID's
// content via its own smaller tool call — a fresh location/npc/
// encounter set tagged with that chapter's id, never campaign.md, which
// already exists and isn't touched by this call. The returned files are
// meant for AddFilesAndValidate, not WriteAndValidate (which creates a
// brand-new pack rather than extending one).
func GenerateChapter(ctx context.Context, provider llm.Provider, model string, req GenerateChapterRequest) ([]GeneratedFile, error) {
	pack, err := LoadPack(req.ExistingPackDir)
	if err != nil {
		return nil, fmt.Errorf("campaignpack: loading existing pack for chapter generation: %w", err)
	}
	var chapter *Chapter
	for i := range pack.Chapters {
		if pack.Chapters[i].ID == req.ChapterID {
			chapter = &pack.Chapters[i]
			break
		}
	}
	if chapter == nil {
		return nil, fmt.Errorf("campaignpack: chapter %q not found in this pack's outline", req.ChapterID)
	}

	var locationIDs, npcIDs []string
	for _, l := range pack.Locations {
		locationIDs = append(locationIDs, l.ID)
	}
	for _, n := range pack.NPCs {
		npcIDs = append(npcIDs, n.ID)
	}

	userPrompt := fmt.Sprintf(
		"Campaign: %s\nPremise: %s\nChapter to write: %s — %q (level range %s)\nChapter summary: %s\nAlready-established location ids: %s\nAlready-established NPC ids: %s",
		pack.Title, pack.Overview, chapter.ID, chapter.Title, chapter.LevelRange, chapter.Summary,
		strings.Join(locationIDs, ", "), strings.Join(npcIDs, ", "),
	)

	files, err := generateFiles(ctx, provider, model, chapterGenerationSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return validateSupplementalFiles(files)
}

// GenerateSideQuestRequest describes a short, self-contained side
// adventure to generate into an existing pack — the concrete Thursday-
// night need this feature exists for: a table short a player, wanting
// something the rest can play without touching the main campaign.
type GenerateSideQuestRequest struct {
	ExistingPackDir string
	Description     string
	MaxPlayers      int
}

// GenerateSideQuest loads the existing pack at req.ExistingPackDir for
// light context (premise/tone only, not the full roster — a side quest
// is meant to stand on its own) and generates a small, self-contained
// side-quest bundle via its own tool call, tagged with a fresh
// side_quest id. Like GenerateChapter, the result is meant for
// AddFilesAndValidate.
func GenerateSideQuest(ctx context.Context, provider llm.Provider, model string, req GenerateSideQuestRequest) ([]GeneratedFile, error) {
	pack, err := LoadPack(req.ExistingPackDir)
	if err != nil {
		return nil, fmt.Errorf("campaignpack: loading existing pack for side quest generation: %w", err)
	}

	userPrompt := fmt.Sprintf(
		"Campaign: %s\nPremise: %s\nSide quest request: %s\nMax players available: %d",
		pack.Title, pack.Overview, req.Description, req.MaxPlayers,
	)

	files, err := generateFiles(ctx, provider, model, sideQuestGenerationSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return validateSupplementalFiles(files)
}

// generateFiles is the shared core behind Generate/GenerateChapter/
// GenerateSideQuest: one structured tool call, returning whatever files
// the model produced. Validation specific to each caller (Generate
// requires campaign.md present; GenerateChapter/GenerateSideQuest
// require it absent) happens in the caller, not here.
func generateFiles(ctx context.Context, provider llm.Provider, model, systemPrompt, userPrompt string) ([]GeneratedFile, error) {
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Tools:        []llm.Tool{writePackTool()},
	})
	if err != nil {
		return nil, fmt.Errorf("campaignpack: generating pack content: %w", err)
	}

	var toolCall *llm.ToolCall
	for i, tc := range resp.ToolCalls {
		if tc.Name == writePackToolName {
			toolCall = &resp.ToolCalls[i]
			break
		}
	}
	if toolCall == nil {
		return nil, fmt.Errorf("campaignpack: model did not generate content (no %s tool call)", writePackToolName)
	}

	files, err := parseGeneratedFiles(toolCall.Arguments)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Content = repairUnquotedColonInScalarValues(files[i].Content)
	}
	return files, nil
}

// yamlKeyValueLine matches a YAML "key: value" line, optionally nested
// under a list item ("- key: value") — the shape every real front-matter
// field in this package's generated files takes. Capture groups: 1 =
// leading whitespace/dash prefix, 2 = the key, 3 = the value.
var yamlKeyValueLine = regexp.MustCompile(`^(\s*(?:-\s+)?)([A-Za-z_][A-Za-z0-9_]*):[ \t](.+)$`)

// repairUnquotedColonInScalarValues fixes a real, live-observed model
// quirk confirmed independently twice (an npc's voice field, and a
// campaign.md chapters[].summary field): a front-matter scalar value
// containing an unescaped ": " sequence — e.g. "voice: precise, like a
// scalpel: not to wound" — which breaks YAML parsing, since the
// embedded colon reads as the start of a nested mapping rather than
// part of the value. Only touches lines inside the front-matter block
// (between the first and second "---" delimiters) — the markdown body
// isn't YAML and a colon there is always fine as-is. Only touches a
// value that isn't already quoted and isn't a block-scalar/flow-
// collection indicator (>, |, [, {) — deliberately narrow, the same
// "only repair the specific confirmed failure shape" restraint
// escapeInvalidJSONBackslashes already documents. Quoting a value that
// didn't strictly need it is harmless (a quoted YAML scalar with no
// special characters is identical to the unquoted form), so this errs
// toward fixing rather than under-matching.
func repairUnquotedColonInScalarValues(content string) string {
	lines := strings.Split(content, "\n")
	delimitersSeen := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			delimitersSeen++
			continue
		}
		if delimitersSeen != 1 {
			continue // before the front matter, or past it into the body
		}
		m := yamlKeyValueLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prefix, key, value := m[1], m[2], m[3]
		if !needsColonRepair(value) {
			continue
		}
		lines[i] = prefix + key + ": " + quoteYAMLScalar(value)
	}
	return strings.Join(lines, "\n")
}

// needsColonRepair reports whether value is an unquoted, non-block-
// scalar YAML value containing an embedded ": " (or a trailing ":")
// that would break parsing if left as-is.
func needsColonRepair(value string) bool {
	if value == "" {
		return false
	}
	switch value[0] {
	case '"', '\'', '>', '|', '[', '{':
		return false
	}
	return strings.Contains(value, ": ") || strings.HasSuffix(value, ":")
}

// quoteYAMLScalar wraps value in a double-quoted YAML scalar, escaping
// the two characters that would otherwise break out of it.
func quoteYAMLScalar(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// validateSupplementalFiles checks the shape required of any generation
// that extends an existing pack rather than creating one (GenerateChapter,
// GenerateSideQuest): at least one file, and never campaign.md — that
// file already exists in the pack being extended and isn't touched by
// these calls.
func validateSupplementalFiles(files []GeneratedFile) ([]GeneratedFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("campaignpack: model generated no files")
	}
	for _, f := range files {
		if f.Path == "campaign.md" {
			return nil, fmt.Errorf("campaignpack: generated content must not include campaign.md (that file already exists in the pack being extended)")
		}
	}
	return files, nil
}

// parseGeneratedFiles decodes the write_campaign_pack tool call's
// "files" field, tolerating a real, live-observed model quirk:
// confirmed against a real local Ollama model that some models
// (particularly local/quantized ones) stringify a nested array instead
// of emitting a genuine JSON array for it, even though the offered
// schema says array — the same class of "arguments shaped differently
// than the schema promised" issue OpenAI's own convention already
// forces this codebase to normalize elsewhere (see
// openai_compatible.go's normalizeOpenAIToolArguments), just one level
// deeper (a field within the arguments, not the arguments themselves).
func parseGeneratedFiles(raw json.RawMessage) ([]GeneratedFile, error) {
	var direct struct {
		Files []GeneratedFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct.Files, nil
	}

	var stringified struct {
		Files string `json:"files"`
	}
	if err := json.Unmarshal(raw, &stringified); err != nil || stringified.Files == "" {
		return nil, fmt.Errorf("campaignpack: decoding generated pack: files field is neither a JSON array nor a JSON-encoded string of one")
	}
	var files []GeneratedFile
	if err := json.Unmarshal([]byte(stringified.Files), &files); err == nil {
		return files, nil
	}
	// Confirmed live, repeatedly, against a real local model: it
	// sometimes writes a backslash before a character that isn't a
	// valid JSON escape (producing e.g. "\i" inside a string), a
	// well-documented class of mistake models make when hand-writing a
	// stringified JSON payload. Escaping that stray backslash itself
	// turns the invalid sequence into a literal backslash-plus-character
	// — the most plausible recovery of what the model meant — and is
	// safe to attempt unconditionally here since it only runs after a
	// first, direct parse has already failed.
	if err := json.Unmarshal([]byte(escapeInvalidJSONBackslashes(stringified.Files)), &files); err != nil {
		return nil, fmt.Errorf("campaignpack: decoding generated pack: files string does not contain valid JSON even after repairing invalid escapes: %w", err)
	}
	return files, nil
}

// escapeInvalidJSONBackslashes doubles every backslash in s that isn't
// already part of a valid JSON escape sequence (\", \\, \/, \b, \f, \n,
// \r, \t, \u) — see parseGeneratedFiles' own doc comment for why this
// exists. Safe to run on already-valid JSON: every backslash that's
// already part of a real escape sequence is left untouched.
func escapeInvalidJSONBackslashes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 < len(s) {
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				b.WriteByte(c)
				continue
			}
		}
		b.WriteString(`\\`)
	}
	return b.String()
}
