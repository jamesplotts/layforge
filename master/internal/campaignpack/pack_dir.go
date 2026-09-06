// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrUnsafePackDir is returned by PackDirFor when slug isn't a safe,
// single path component — see its own doc comment for why this check
// exists independent of SanitizeSlug.
var ErrUnsafePackDir = errors.New("campaignpack: resulting path escapes the configured root")

// maxSlugLen bounds a generated pack's directory name — generous for
// any real adventure title, short enough to stay a sane filesystem path
// component.
const maxSlugLen = 80

var slugInvalidRun = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeSlug turns a Host-provided or description-derived string into
// a safe directory name: lowercased, every run of non-alphanumeric
// characters collapsed to a single hyphen, leading/trailing hyphens
// trimmed, capped at maxSlugLen. Returns an error for an input that
// sanitizes to an empty string (e.g. all punctuation).
func SanitizeSlug(s string) (string, error) {
	lower := strings.ToLower(s)
	collapsed := slugInvalidRun.ReplaceAllString(lower, "-")
	trimmed := strings.Trim(collapsed, "-")
	if trimmed == "" {
		return "", fmt.Errorf("campaignpack: %q sanitizes to an empty slug", s)
	}
	if len(trimmed) > maxSlugLen {
		trimmed = strings.Trim(trimmed[:maxSlugLen], "-")
		if trimmed == "" {
			return "", fmt.Errorf("campaignpack: %q sanitizes to an empty slug after length capping", s)
		}
	}
	return trimmed, nil
}

// PackDirFor joins slug onto root for a generated pack's own directory.
// slug must be a single, safe path component — this is checked directly
// (no separators, not "." or ".."), rather than relying on
// filepath.Join/Rel to catch a traversal attempt after the fact: this is
// the one place in this package that writes files based on
// model-influenced input rather than direct Host input (see
// GenerateRequest/WriteAndValidate), so the check has to hold even if a
// caller reaches this function with a slug that skipped SanitizeSlug —
// defense in depth, not a single point of failure.
func PackDirFor(root, slug string) (string, error) {
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, "/\\") {
		return "", ErrUnsafePackDir
	}
	return filepath.Join(root, slug), nil
}
