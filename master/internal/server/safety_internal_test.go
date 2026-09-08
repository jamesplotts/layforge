// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func newSafetyTestServer(t *testing.T) (*Server, *store.SQLiteEventStore) {
	t.Helper()
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		events:       st,
		campaignPack: st,
		safety:       st,
		hub:          session.NewHub(),
	}, st
}

func bindPackWithLinesAndVeils(t *testing.T, st *store.SQLiteEventStore, campaignID string) {
	t.Helper()
	dir, err := campaignpack.WriteAndValidate(t.TempDir(), "safety-pack", []campaignpack.GeneratedFile{
		{Path: "campaign.md", Content: "---\nid: safety-pack\ntitle: Test\nlines:\n  - \"no harm to children\"\nveils:\n  - \"off-screen: executions\"\n---\nOverview.\n"},
	})
	if err != nil {
		t.Fatalf("WriteAndValidate() error = %v", err)
	}
	if err := st.SaveCampaignPack(context.Background(), campaignID, dir, "safety-pack"); err != nil {
		t.Fatalf("SaveCampaignPack() error = %v", err)
	}
}

func TestSafetyConstraintsContextText_NothingInEffect_ReturnsEmpty(t *testing.T) {
	srv, _ := newSafetyTestServer(t)
	if got := srv.safetyConstraintsContextText(context.Background(), "campaign-1"); got != "" {
		t.Errorf("safetyConstraintsContextText() = %q, want empty", got)
	}
}

func TestSafetyConstraintsContextText_RaisedFlagsOnly(t *testing.T) {
	srv, st := newSafetyTestServer(t)
	if err := st.AddSafetyFlag(context.Background(), "campaign-1", "spiders"); err != nil {
		t.Fatal(err)
	}

	got := srv.safetyConstraintsContextText(context.Background(), "campaign-1")
	if !strings.Contains(got, "Safety constraints") {
		t.Errorf("missing header:\n%s", got)
	}
	if !strings.Contains(got, "invoked the safety tool") || !strings.Contains(got, "- spiders") {
		t.Errorf("missing raised-flag section or topic:\n%s", got)
	}
	if strings.Contains(got, "off-screen") {
		t.Errorf("veil section present with no pack bound:\n%s", got)
	}
}

func TestSafetyConstraintsContextText_PackLinesAndVeilsOnly(t *testing.T) {
	srv, st := newSafetyTestServer(t)
	bindPackWithLinesAndVeils(t, st, "campaign-1")

	got := srv.safetyConstraintsContextText(context.Background(), "campaign-1")
	if !strings.Contains(got, "- no harm to children") {
		t.Errorf("missing line:\n%s", got)
	}
	if !strings.Contains(got, "- off-screen: executions") {
		t.Errorf("missing veil:\n%s", got)
	}
	if !strings.Contains(got, "Keep entirely off-screen") {
		t.Errorf("missing veil label:\n%s", got)
	}
	if strings.Contains(got, "invoked the safety tool") {
		t.Errorf("raised-flag section present with no flags raised:\n%s", got)
	}
}

func TestSafetyConstraintsContextText_PackAndRaisedFlagsCombined(t *testing.T) {
	srv, st := newSafetyTestServer(t)
	bindPackWithLinesAndVeils(t, st, "campaign-1")
	if err := st.AddSafetyFlag(context.Background(), "campaign-1", "clowns"); err != nil {
		t.Fatal(err)
	}

	got := srv.safetyConstraintsContextText(context.Background(), "campaign-1")
	for _, want := range []string{"- no harm to children", "- off-screen: executions", "- clowns"} {
		if !strings.Contains(got, want) {
			t.Errorf("combined output missing %q:\n%s", want, got)
		}
	}
}

func TestSafetyConstraintsContextText_NilSafetyStore_StillReturnsPackConstraints(t *testing.T) {
	srv, st := newSafetyTestServer(t)
	srv.safety = nil
	bindPackWithLinesAndVeils(t, st, "campaign-1")

	got := srv.safetyConstraintsContextText(context.Background(), "campaign-1")
	if !strings.Contains(got, "- no harm to children") {
		t.Errorf("pack constraints should still apply with a nil safety store:\n%s", got)
	}
}

func TestBroadcastSafetyFlag_WithTopic_PersistsStandingConstraint(t *testing.T) {
	srv, st := newSafetyTestServer(t)

	if err := srv.broadcastSafetyFlag(context.Background(), "campaign-1", "on-screen torture"); err != nil {
		t.Fatalf("broadcastSafetyFlag() error = %v", err)
	}

	topics, err := st.ListSafetyFlags(context.Background(), "campaign-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 || topics[0] != "on-screen torture" {
		t.Errorf("ListSafetyFlags() = %v, want [on-screen torture]", topics)
	}
}

func TestBroadcastSafetyFlag_Topicless_PersistsNothing(t *testing.T) {
	srv, st := newSafetyTestServer(t)

	if err := srv.broadcastSafetyFlag(context.Background(), "campaign-1", ""); err != nil {
		t.Fatalf("broadcastSafetyFlag() error = %v", err)
	}

	topics, err := st.ListSafetyFlags(context.Background(), "campaign-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 0 {
		t.Errorf("a topicless flag should persist nothing, got %v", topics)
	}
}

func TestSlowPassGroundingContext_LeadsWithSafetyConstraints(t *testing.T) {
	srv, st := newSafetyTestServer(t)
	if err := st.AddSafetyFlag(context.Background(), "campaign-1", "spiders"); err != nil {
		t.Fatal(err)
	}

	got := srv.slowPassGroundingContext(context.Background(), "campaign-1", protocol.NarrativePlayerInputMessage{
		Payload: protocol.NarrativePlayerInputPayload{CharacterID: "char-1", Text: "I look around."},
	})
	if !strings.HasPrefix(got, "Safety constraints") {
		t.Errorf("grounding context should lead with safety constraints:\n%s", got)
	}
	if !strings.Contains(got, "Character ID: char-1") || !strings.Contains(got, "Player action: I look around.") {
		t.Errorf("grounding context lost its other sections:\n%s", got)
	}
}
