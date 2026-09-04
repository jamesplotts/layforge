// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadPack reads a campaign pack directory (design doc §6.4) — dir must
// contain a campaign.md, and may contain locations/, npcs/, and
// encounters/ subdirectories of *.md files, each in the front matter +
// body shape splitFrontMatter parses. Every location, NPC, and
// encounter must have a real id — an empty one is a real rejection,
// since every DM tool built on this package looks entries up by id.
// Files within each subdirectory are loaded in sorted filename order,
// for deterministic results.
func LoadPack(dir string) (Pack, error) {
	campaignBytes, err := os.ReadFile(filepath.Join(dir, "campaign.md"))
	if err != nil {
		return Pack{}, fmt.Errorf("campaignpack: reading campaign.md: %w", err)
	}
	var campaignFrontMatter struct {
		ID              string   `yaml:"id"`
		Title           string   `yaml:"title"`
		LevelRange      string   `yaml:"level_range"`
		Tone            []string `yaml:"tone"`
		PvPPolicy       string   `yaml:"pvp_policy"`
		MaturityTier    string   `yaml:"maturity_tier"`
		SharedKnowledge string   `yaml:"shared_knowledge"`
		Lines           []string `yaml:"lines"`
		Veils           []string `yaml:"veils"`
		Author          string   `yaml:"author"`
		ContentWarnings []string `yaml:"content_warnings"`
	}
	overview, err := parseFrontMatter(campaignBytes, &campaignFrontMatter)
	if err != nil {
		return Pack{}, fmt.Errorf("campaignpack: parsing campaign.md: %w", err)
	}
	if campaignFrontMatter.ID == "" {
		return Pack{}, fmt.Errorf("campaignpack: campaign.md front matter has no id")
	}

	pack := Pack{
		ID:              campaignFrontMatter.ID,
		Title:           campaignFrontMatter.Title,
		LevelRange:      campaignFrontMatter.LevelRange,
		Tone:            campaignFrontMatter.Tone,
		PvPPolicy:       campaignFrontMatter.PvPPolicy,
		MaturityTier:    campaignFrontMatter.MaturityTier,
		SharedKnowledge: campaignFrontMatter.SharedKnowledge,
		Lines:           campaignFrontMatter.Lines,
		Veils:           campaignFrontMatter.Veils,
		Author:          campaignFrontMatter.Author,
		ContentWarnings: campaignFrontMatter.ContentWarnings,
		Overview:        overview,
	}

	locationFiles, err := sortedMarkdownFiles(filepath.Join(dir, "locations"))
	if err != nil {
		return Pack{}, err
	}
	for _, path := range locationFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: reading %s: %w", path, err)
		}
		var frontMatter struct {
			ID          string   `yaml:"id"`
			Connections []string `yaml:"connections"`
		}
		body, err := parseFrontMatter(data, &frontMatter)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: parsing %s: %w", path, err)
		}
		if frontMatter.ID == "" {
			return Pack{}, fmt.Errorf("campaignpack: %s front matter has no id", path)
		}
		pack.Locations = append(pack.Locations, Location{
			ID:          frontMatter.ID,
			Connections: frontMatter.Connections,
			Body:        body,
		})
	}

	npcFiles, err := sortedMarkdownFiles(filepath.Join(dir, "npcs"))
	if err != nil {
		return Pack{}, err
	}
	for _, path := range npcFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: reading %s: %w", path, err)
		}
		var frontMatter struct {
			ID           string `yaml:"id"`
			Location     string `yaml:"location"`
			StatBlockRef string `yaml:"stat_block_ref"`
			Voice        string `yaml:"voice"`
		}
		body, err := parseFrontMatter(data, &frontMatter)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: parsing %s: %w", path, err)
		}
		if frontMatter.ID == "" {
			return Pack{}, fmt.Errorf("campaignpack: %s front matter has no id", path)
		}
		pack.NPCs = append(pack.NPCs, NPC{
			ID:           frontMatter.ID,
			Location:     frontMatter.Location,
			StatBlockRef: frontMatter.StatBlockRef,
			Voice:        frontMatter.Voice,
			Body:         body,
		})
	}

	encounterFiles, err := sortedMarkdownFiles(filepath.Join(dir, "encounters"))
	if err != nil {
		return Pack{}, err
	}
	for _, path := range encounterFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: reading %s: %w", path, err)
		}
		var frontMatter struct {
			ID       string   `yaml:"id"`
			Location string   `yaml:"location"`
			Involves []string `yaml:"involves"`
		}
		body, err := parseFrontMatter(data, &frontMatter)
		if err != nil {
			return Pack{}, fmt.Errorf("campaignpack: parsing %s: %w", path, err)
		}
		if frontMatter.ID == "" {
			return Pack{}, fmt.Errorf("campaignpack: %s front matter has no id", path)
		}
		pack.Encounters = append(pack.Encounters, Encounter{
			ID:       frontMatter.ID,
			Location: frontMatter.Location,
			Involves: frontMatter.Involves,
			Body:     body,
		})
	}

	return pack, nil
}

// sortedMarkdownFiles returns the full paths of every *.md file directly
// inside dir, sorted by filename. A missing dir (a pack with no
// locations/npcs/encounters at all) returns an empty, non-error result
// — only campaign.md itself is mandatory.
func sortedMarkdownFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("campaignpack: reading %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths, nil
}

// parseFrontMatter splits content into a YAML front matter block
// (delimited by "---" lines, unmarshaled into frontMatter) and a
// markdown body, returned with leading/trailing whitespace trimmed.
func parseFrontMatter(content []byte, frontMatter any) (body string, err error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", fmt.Errorf("must start with a \"---\" front matter delimiter")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		rawFrontMatter := strings.Join(lines[1:i], "\n")
		if err := yaml.Unmarshal([]byte(rawFrontMatter), frontMatter); err != nil {
			return "", fmt.Errorf("parsing front matter: %w", err)
		}
		return strings.TrimSpace(strings.Join(lines[i+1:], "\n")), nil
	}
	return "", fmt.Errorf("front matter has no closing \"---\" delimiter")
}
