// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package campaignpack_test

import (
	"errors"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
)

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "AlreadyClean", input: "haunted-lighthouse", want: "haunted-lighthouse"},
		{name: "SpacesAndPunctuation", input: "A Haunted Lighthouse!", want: "a-haunted-lighthouse"},
		{name: "CollapsesRepeatedSeparators", input: "the   ravine -- of sable", want: "the-ravine-of-sable"},
		{name: "TrimsLeadingTrailingSeparators", input: "  --lighthouse--  ", want: "lighthouse"},
		{name: "PathTraversalLooking", input: "../../etc/passwd", want: "etc-passwd"},
		{name: "OnlyPunctuation_Errors", input: "!!!", wantErr: true},
		{name: "Empty_Errors", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := campaignpack.SanitizeSlug(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SanitizeSlug(%q) error = nil, want an error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("SanitizeSlug(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("SanitizeSlug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPackDirFor(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		slug    string
		want    string
		wantErr bool
	}{
		{name: "SimpleSlug", root: "/data/packs", slug: "haunted-lighthouse", want: "/data/packs/haunted-lighthouse"},
		{
			name:    "SlugWithTraversal_Rejected",
			root:    "/data/packs",
			slug:    "../../etc",
			wantErr: true,
		},
		{
			name:    "AbsoluteSlug_Rejected",
			root:    "/data/packs",
			slug:    "/etc/passwd",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := campaignpack.PackDirFor(tt.root, tt.slug)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PackDirFor(%q, %q) error = nil, want an error (dir = %q)", tt.root, tt.slug, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PackDirFor(%q, %q) error = %v", tt.root, tt.slug, err)
			}
			if got != tt.want {
				t.Errorf("PackDirFor(%q, %q) = %q, want %q", tt.root, tt.slug, got, tt.want)
			}
		})
	}
}

func TestPackDirFor_DefenseInDepth_EvenASlugThatSomehowSkippedSanitizeSlugIsRejected(t *testing.T) {
	// PackDirFor must reject a traversal attempt on its own, independent
	// of SanitizeSlug ever having run — see this package's own doc
	// comment on why this is deliberately not a single point of failure.
	_, err := campaignpack.PackDirFor("/data/packs", "..")
	if !errors.Is(err, campaignpack.ErrUnsafePackDir) {
		t.Errorf("PackDirFor() error = %v, want ErrUnsafePackDir", err)
	}
}
