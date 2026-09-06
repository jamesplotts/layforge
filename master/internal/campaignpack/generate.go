// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"context"
	"encoding/json"
	"fmt"
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

Call write_campaign_pack exactly once with every file. Produce:
- campaign.md: YAML front matter with id (a short lowercase-hyphenated slug), title, level_range (e.g. "1-3"), pvp_policy (one of pve_only, pvp_allowed — default pve_only unless the description implies otherwise), maturity_tier ("standard" unless the description clearly implies otherwise), image_maturity_tier ("family_friendly" unless the description clearly implies otherwise), shared_knowledge ("strict" unless the description implies otherwise), tone (a short list of adjectives), author ("AI-generated"). Body: a few paragraphs of overview prose plus a "## Hooks" section.
- locations/*.md (3-5 files): YAML front matter with id and connections (a list of other location ids directly reachable from this one — build a real, traversable graph). Body: a few paragraphs of prose description.
- npcs/*.md (2-4 files): YAML front matter with id, location (which location id they're normally found at), stat_block_ref (a plain SRD creature/class type, e.g. "SRD Veteran" or "SRD Commoner" — never a named published monster), voice (a short phrase describing how they speak). Body: a paragraph of personality/motivation.
- encounters/*.md (2-3 files): YAML front matter with id, location, involves (a list of the npc ids present). Body: a paragraph describing the situation and how it might unfold.

Every id across every file must be unique and lowercase-hyphenated. Keep the whole pack small and focused — enough for one real short adventure, not a sprawling setting.`

// Generate calls provider once with a single structured tool call and
// returns the files it produced. It does not write anything to disk —
// see WriteAndValidate for that. A structured tool call (rather than
// hoping a plain completion emits well-formed multi-file markdown with
// correct front-matter delimiters) mirrors the same "structured JSON
// arguments via a tool call" pattern internal/server/dm_tools.go
// already relies on for reliability.
func Generate(ctx context.Context, provider llm.Provider, model string, req GenerateRequest) ([]GeneratedFile, error) {
	userPrompt := fmt.Sprintf("Adventure description: %s\nLevel range: %d-%d", req.Description, req.MinLevel, req.MaxLevel)

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:        model,
		SystemPrompt: campaignGenerationSystemPrompt,
		UserPrompt:   userPrompt,
		Tools:        []llm.Tool{writePackTool()},
	})
	if err != nil {
		return nil, fmt.Errorf("campaignpack: generating pack: %w", err)
	}

	var toolCall *llm.ToolCall
	for i, tc := range resp.ToolCalls {
		if tc.Name == writePackToolName {
			toolCall = &resp.ToolCalls[i]
			break
		}
	}
	if toolCall == nil {
		return nil, fmt.Errorf("campaignpack: model did not generate a campaign pack (no %s tool call)", writePackToolName)
	}

	files, err := parseGeneratedFiles(toolCall.Arguments)
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
