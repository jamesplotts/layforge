// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestSafetyFlags_AddThenList_ReturnsInInsertionOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, topic := range []string{"spiders", "on-screen torture", "harm to animals"} {
		if err := s.AddSafetyFlag(ctx, "camp-1", topic); err != nil {
			t.Fatalf("AddSafetyFlag(%q) error = %v", topic, err)
		}
	}

	got, err := s.ListSafetyFlags(ctx, "camp-1")
	if err != nil {
		t.Fatalf("ListSafetyFlags() error = %v", err)
	}
	want := []string{"spiders", "on-screen torture", "harm to animals"}
	if len(got) != len(want) {
		t.Fatalf("ListSafetyFlags() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListSafetyFlags()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSafetyFlags_ReflaggingSameTopic_IsANoOp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AddSafetyFlag(ctx, "camp-1", "spiders"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSafetyFlag(ctx, "camp-1", "spiders"); err != nil {
		t.Fatalf("re-adding an existing topic should not error, got %v", err)
	}

	got, err := s.ListSafetyFlags(ctx, "camp-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("ListSafetyFlags() = %v, want exactly one entry", got)
	}
}

func TestSafetyFlags_ScopedByCampaign(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AddSafetyFlag(ctx, "camp-1", "spiders"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSafetyFlag(ctx, "camp-2", "heights"); err != nil {
		t.Fatal(err)
	}

	got1, _ := s.ListSafetyFlags(ctx, "camp-1")
	if len(got1) != 1 || got1[0] != "spiders" {
		t.Errorf("camp-1 flags = %v, want [spiders]", got1)
	}
	got2, _ := s.ListSafetyFlags(ctx, "camp-2")
	if len(got2) != 1 || got2[0] != "heights" {
		t.Errorf("camp-2 flags = %v, want [heights]", got2)
	}
}

func TestSafetyFlags_UnknownCampaign_ReturnsEmpty(t *testing.T) {
	s := newTestStore(t)

	got, err := s.ListSafetyFlags(context.Background(), "never-flagged")
	if err != nil {
		t.Fatalf("ListSafetyFlags() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListSafetyFlags() = %v, want empty", got)
	}
}

func TestSafetyFlags_EmptyCampaignID_Rejected(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddSafetyFlag(context.Background(), "", "spiders"); !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("AddSafetyFlag(\"\") error = %v, want ErrCampaignIDRequired", err)
	}
}
