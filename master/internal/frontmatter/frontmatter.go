// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package frontmatter parses the "markdown + YAML front matter" file
// shape design doc §6.4/§6.5 use for every host-authored content type
// this project loads from disk (campaign packs, maturity tiers) —
// extracted here once both needed the identical split-and-unmarshal
// logic, rather than each content-loading package repeating it.
package frontmatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse splits content into a YAML front matter block (delimited by
// "---" lines, unmarshaled into frontMatter) and a markdown body,
// returned with leading/trailing whitespace trimmed.
func Parse(content []byte, frontMatter any) (body string, err error) {
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
