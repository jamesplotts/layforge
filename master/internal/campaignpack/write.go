// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// allowedGeneratedFilePath is the strict allow-list every GeneratedFile
// path must match before anything is written — these paths originate
// from model-influenced content (Generate), not direct Host input, so
// they're validated the same way any other untrusted input would be,
// not trusted the way an operator-typed PackDir already is elsewhere in
// this codebase. Rejects absolute paths, ".." segments, and anything
// outside the four files/directories a real pack actually uses.
var allowedGeneratedFilePath = regexp.MustCompile(`^(campaign\.md|(?:locations|npcs|encounters)/[a-z0-9][a-z0-9-]*\.md)$`)

// WriteAndValidate writes files under root/slug (see PackDirFor for the
// sandboxing this depends on), then runs the real LoadPack against the
// result — the exact same gate a hand-authored pack goes through when a
// Host binds it via PUT /api/campaigns/{id}/pack — deleting everything
// it just wrote if any path is disallowed or the pack fails to parse,
// so a bad generation never leaves a half-written directory behind.
// Returns the pack directory path on success, ready to hand straight to
// that same existing bind endpoint.
func WriteAndValidate(root, slug string, files []GeneratedFile) (string, error) {
	dir, err := PackDirFor(root, slug)
	if err != nil {
		return "", err
	}

	for _, f := range files {
		if !allowedGeneratedFilePath.MatchString(f.Path) {
			return "", fmt.Errorf("campaignpack: disallowed generated file path %q", f.Path)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("campaignpack: creating pack directory: %w", err)
	}
	for _, f := range files {
		full := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("campaignpack: creating directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("campaignpack: writing %s: %w", f.Path, err)
		}
	}

	if _, err := LoadPack(dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("campaignpack: generated pack does not parse: %w", err)
	}
	return dir, nil
}
