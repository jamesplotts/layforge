// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package maturitytiers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jamesplotts/layforge/master/internal/frontmatter"
)

// LoadTiers reads every *.md file directly inside dir — each one a
// tier definition, front matter + body (see Tier) — into a map keyed by
// tier id. Unlike campaignpack.LoadPack's optional locations/npcs/
// encounters subdirectories, dir itself is not optional here: a caller
// only calls LoadTiers at all once a host has configured a tiers
// directory, so a missing or unreadable one is a real misconfiguration,
// not "no tiers today." Every tier must have a real, unique id — an
// empty or duplicate one is a real rejection, since campaign.md's own
// maturity_tier field looks tiers up by id.
func LoadTiers(dir string) (map[string]Tier, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("maturitytiers: reading %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		// README.md documents the directory itself, not a tier — same
		// distinction campaign-packs/README.md already draws relative to
		// individual pack directories, just collapsed into one flat
		// directory here instead of a parent/child split.
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") && entry.Name() != "README.md" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	tiers := make(map[string]Tier, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("maturitytiers: reading %s: %w", path, err)
		}
		var fm struct {
			ID          string `yaml:"id"`
			DisplayName string `yaml:"display_name"`
			Rank        int    `yaml:"rank"`
		}
		body, err := frontmatter.Parse(data, &fm)
		if err != nil {
			return nil, fmt.Errorf("maturitytiers: parsing %s: %w", path, err)
		}
		if fm.ID == "" {
			return nil, fmt.Errorf("maturitytiers: %s front matter has no id", path)
		}
		if _, exists := tiers[fm.ID]; exists {
			return nil, fmt.Errorf("maturitytiers: duplicate tier id %q (in %s)", fm.ID, path)
		}
		tiers[fm.ID] = Tier{
			ID:          fm.ID,
			DisplayName: fm.DisplayName,
			Rank:        fm.Rank,
			Prompt:      body,
		}
	}
	return tiers, nil
}
