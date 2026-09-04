// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package frontmatter_test

import (
	"testing"

	"github.com/jamesplotts/layforge/master/internal/frontmatter"
)

func TestParse_ValidFrontMatterAndBody_ParsesBoth(t *testing.T) {
	var got struct {
		ID   string `yaml:"id"`
		Rank int    `yaml:"rank"`
	}
	body, err := frontmatter.Parse([]byte("---\nid: test\nrank: 2\n---\nHello, body.\n"), &got)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.ID != "test" || got.Rank != 2 {
		t.Errorf("front matter = %+v, want ID=test Rank=2", got)
	}
	if body != "Hello, body." {
		t.Errorf("body = %q, want %q", body, "Hello, body.")
	}
}

func TestParse_MissingOpeningDelimiter_ReturnsError(t *testing.T) {
	var got struct{}
	_, err := frontmatter.Parse([]byte("id: test\n---\nbody\n"), &got)
	if err == nil {
		t.Fatal("Parse() error = nil, want an error")
	}
}

func TestParse_MissingClosingDelimiter_ReturnsError(t *testing.T) {
	var got struct{}
	_, err := frontmatter.Parse([]byte("---\nid: test\nbody with no closing delimiter\n"), &got)
	if err == nil {
		t.Fatal("Parse() error = nil, want an error")
	}
}

func TestParse_CRLFLineEndings_ParsesTheSameAsLF(t *testing.T) {
	var got struct {
		ID string `yaml:"id"`
	}
	body, err := frontmatter.Parse([]byte("---\r\nid: test\r\n---\r\nbody\r\n"), &got)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.ID != "test" {
		t.Errorf("ID = %q, want %q", got.ID, "test")
	}
	if body != "body" {
		t.Errorf("body = %q, want %q", body, "body")
	}
}
