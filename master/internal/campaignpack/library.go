// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InstallStatus is the outcome of installing one pack from a downloaded
// library archive. Its zero value is InstallStatusUnspecified, which
// IsValid rejects — a real result always carries one of the three
// defined outcomes.
type InstallStatus string

const (
	// InstallStatusUnspecified is the zero value and never a valid
	// outcome.
	InstallStatusUnspecified InstallStatus = ""
	// InstallStatusInstalled means the pack parsed via LoadPack and its
	// directory now exists under the destination root (replacing a
	// previous one only when the caller asked for that).
	InstallStatusInstalled InstallStatus = "installed"
	// InstallStatusSkippedExists means a directory for this slug already
	// existed and InstallOptions.Overwrite was false, so nothing was
	// touched.
	InstallStatusSkippedExists InstallStatus = "skipped_exists"
	// InstallStatusFailed means the archive's copy of this pack did not
	// parse via LoadPack (or could not be extracted). Nothing was written
	// for it; the other packs in the archive were still processed. Detail
	// carries the reason.
	InstallStatusFailed InstallStatus = "failed"
)

// IsValid reports whether s is one of the three defined outcomes and not
// the unspecified zero value.
func (s InstallStatus) IsValid() bool {
	switch s {
	case InstallStatusInstalled, InstallStatusSkippedExists, InstallStatusFailed:
		return true
	default:
		return false
	}
}

// InstallResult is the per-pack outcome of InstallLibrary — one entry per
// pack directory found in the archive, returned sorted by Slug.
type InstallResult struct {
	Slug   string
	Status InstallStatus
	// Detail is a human-readable explanation for InstallStatusFailed and
	// is empty otherwise.
	Detail string
}

// InstallOptions controls InstallLibrary's behavior.
type InstallOptions struct {
	// Overwrite replaces an existing pack directory of the same slug with
	// the archive's copy. When false (the default), an existing slug is
	// left untouched and reported as InstallStatusSkippedExists.
	Overwrite bool
}

// Archive limits guarding against a malicious or corrupt library
// archive. A real pack library is a few dozen small markdown files;
// these bounds are generous relative to that but finite.
const (
	// maxArchiveBytes caps the compressed archive size InstallLibrary
	// accepts. The admin endpoint enforces the identical cap on the HTTP
	// download before the bytes ever reach here.
	maxArchiveBytes = 32 << 20
	// maxUncompressedBytes caps the combined uncompressed size of every
	// entry — a zip-bomb guard independent of maxArchiveBytes.
	maxUncompressedBytes = 128 << 20
	// maxFileBytes caps one entry's uncompressed size. A campaign-pack
	// markdown file is a few KB; 1 MiB is well beyond any real one.
	maxFileBytes = 1 << 20
	// maxFiles caps the entry count across the whole archive.
	maxFiles = 5000
)

// ErrEmptyLibraryArchive is returned by InstallLibrary when the archive
// contains no recognizable pack directories at all.
var ErrEmptyLibraryArchive = errors.New("campaignpack: library archive contains no packs")

// packArchiveEntry is one validated archive member, paired with the pack
// slug it belongs to during InstallLibrary's grouping pass.
type packArchiveEntry struct {
	rel  string // pack-relative path, forward-slashed (e.g. "npcs/host.md")
	file *zip.File
}

// InstallLibrary unpacks an already-downloaded pack-library archive into
// destRoot, one directory per pack, keeping a pack only if its files
// re-parse via LoadPack — the same validation gate WriteAndValidate and
// the PUT /api/campaigns/{id}/pack bind endpoint apply. It performs no
// network I/O; the caller fetches the archive and is responsible for
// enforcing maxArchiveBytes on that download.
//
// The archive is expected to hold each pack at its own root, e.g.
// "the-masquerade/campaign.md" and "the-masquerade/npcs/host.md". Every
// entry path is validated before anything is written: a path that is
// absolute, contains a "." or ".." segment, names a non-canonical slug
// (see SanitizeSlug), or falls outside the pack file allow-list
// (IsAllowedPackFilePath) fails the whole call with nothing written.
// Per-entry, total-size, and entry-count caps guard against a zip bomb.
//
// A structural problem with the archive returns a non-nil error and
// writes nothing. Once the archive is structurally sound, packs are
// installed independently: one pack failing to parse is reported as
// InstallStatusFailed in the results without stopping the others, and
// the returned error is nil.
func InstallLibrary(r io.ReaderAt, size int64, destRoot string, opts InstallOptions) ([]InstallResult, error) {
	if destRoot == "" {
		return nil, errors.New("campaignpack: destination root is required")
	}
	if size <= 0 {
		return nil, errors.New("campaignpack: archive is empty")
	}
	if size > maxArchiveBytes {
		return nil, fmt.Errorf("campaignpack: archive is %d bytes, over the %d-byte limit", size, int64(maxArchiveBytes))
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("campaignpack: reading library archive: %w", err)
	}

	bySlug := map[string][]packArchiveEntry{}
	var totalUncompressed uint64
	fileCount := 0

	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		fileCount++
		if fileCount > maxFiles {
			return nil, fmt.Errorf("campaignpack: archive has more than %d files", maxFiles)
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("campaignpack: unsafe archive entry path %q", name)
		}
		parts := strings.Split(name, "/")
		for _, p := range parts {
			if p == "" || p == "." || p == ".." {
				return nil, fmt.Errorf("campaignpack: unsafe archive entry path %q", name)
			}
		}
		if len(parts) < 2 {
			return nil, fmt.Errorf("campaignpack: archive entry %q is not inside a pack directory", name)
		}
		slug := parts[0]
		if canonical, err := SanitizeSlug(slug); err != nil || canonical != slug {
			return nil, fmt.Errorf("campaignpack: archive entry %q has a non-canonical pack directory name", name)
		}
		if _, err := PackDirFor(destRoot, slug); err != nil {
			return nil, fmt.Errorf("campaignpack: archive entry %q: %w", name, err)
		}
		rel := strings.Join(parts[1:], "/")
		if !IsAllowedPackFilePath(rel) {
			return nil, fmt.Errorf("campaignpack: archive entry %q is not an allowed pack file", name)
		}
		if f.UncompressedSize64 > maxFileBytes {
			return nil, fmt.Errorf("campaignpack: archive entry %q is %d bytes, over the %d-byte per-file limit", name, f.UncompressedSize64, int64(maxFileBytes))
		}
		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > maxUncompressedBytes {
			return nil, fmt.Errorf("campaignpack: archive expands to more than %d bytes", int64(maxUncompressedBytes))
		}
		bySlug[slug] = append(bySlug[slug], packArchiveEntry{rel: rel, file: f})
	}

	if len(bySlug) == 0 {
		return nil, ErrEmptyLibraryArchive
	}

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return nil, fmt.Errorf("campaignpack: creating destination root: %w", err)
	}

	slugs := make([]string, 0, len(bySlug))
	for slug := range bySlug {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	results := make([]InstallResult, 0, len(slugs))
	for _, slug := range slugs {
		results = append(results, installOnePack(destRoot, slug, bySlug[slug], opts))
	}
	return results, nil
}

// installOnePack extracts one pack's validated entries into a temp
// directory inside destRoot, validates the result with LoadPack, and —
// only then — moves it into place. The temp directory lives inside
// destRoot so the final move is an atomic, same-filesystem rename; it is
// always cleaned up, whether the install succeeds, is skipped, or fails.
func installOnePack(destRoot, slug string, entries []packArchiveEntry, opts InstallOptions) InstallResult {
	fail := func(detail string) InstallResult {
		return InstallResult{Slug: slug, Status: InstallStatusFailed, Detail: detail}
	}

	tmp, err := os.MkdirTemp(destRoot, ".install-"+slug+"-*")
	if err != nil {
		return fail("creating a staging directory: " + err.Error())
	}
	defer os.RemoveAll(tmp) // a no-op once a successful rename has moved it

	for _, e := range entries {
		full := filepath.Join(tmp, filepath.FromSlash(e.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fail(err.Error())
		}
		if err := copyZipEntry(e.file, full); err != nil {
			return fail(err.Error())
		}
	}

	if _, err := LoadPack(tmp); err != nil {
		return fail(err.Error())
	}

	dest, err := PackDirFor(destRoot, slug)
	if err != nil {
		return fail(err.Error())
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		if !opts.Overwrite {
			return InstallResult{Slug: slug, Status: InstallStatusSkippedExists}
		}
		if err := os.RemoveAll(dest); err != nil {
			return fail("replacing the existing pack: " + err.Error())
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fail(statErr.Error())
	}

	if err := os.Rename(tmp, dest); err != nil {
		return fail("moving the pack into place: " + err.Error())
	}
	return InstallResult{Slug: slug, Status: InstallStatusInstalled}
}

// copyZipEntry writes one archive member to dest, capping the copy at
// maxFileBytes as a second guard against a header that understates the
// real size (the first-pass check trusts f.UncompressedSize64).
func copyZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, io.LimitReader(rc, maxFileBytes)); err != nil {
		return err
	}
	return out.Close()
}
