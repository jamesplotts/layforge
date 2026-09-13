// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"io"
	"log/slog"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// newClientRollTestServer builds a minimal real *Server for exercising
// client_roll.go's own registry/reveal/finish logic directly — a real
// (if disconnected) Hub and logger, since finishRoll/revealDie call
// through both.
func newClientRollTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		hub:          session.NewHub(),
		pendingRolls: make(map[string]*pendingRoll),
	}
}

func newTestPendingRoll(campaignID string, dice ...protocol.ClientRollDie) *pendingRoll {
	return &pendingRoll{
		campaignID:  campaignID,
		ownerSender: "player-a",
		dice:        dice,
		revealed:    make(map[string]bool, len(dice)),
		total:       14,
		done:        make(chan struct{}),
	}
}

func TestPendingRoll_RevealDie_MarksRevealedAndReportsRemaining(t *testing.T) {
	s := newClientRollTestServer(t)
	pr := newTestPendingRoll("campaign-1",
		protocol.ClientRollDie{ID: "d1", Sides: 20, Result: 11},
		protocol.ClientRollDie{ID: "d2", Sides: 20, Result: 6},
	)

	remaining, err := s.revealDie("prompt-1", pr, "d1")
	if err != nil {
		t.Fatalf("revealDie() error = %v", err)
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1 (one die still hidden)", remaining)
	}
	if !pr.revealed["d1"] {
		t.Error("d1 not marked revealed")
	}
	if pr.revealed["d2"] {
		t.Error("d2 marked revealed, want still hidden")
	}

	remaining, err = s.revealDie("prompt-1", pr, "d2")
	if err != nil {
		t.Fatalf("revealDie() second call error = %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0 (all dice revealed)", remaining)
	}
}

func TestPendingRoll_RevealDie_UnknownDieID_Errors(t *testing.T) {
	s := newClientRollTestServer(t)
	pr := newTestPendingRoll("campaign-1", protocol.ClientRollDie{ID: "d1", Sides: 20, Result: 11})

	if _, err := s.revealDie("prompt-1", pr, "does-not-exist"); err == nil {
		t.Fatal("revealDie() error = nil, want an error for an unknown die_id")
	}
}

func TestPendingRoll_RevealDie_AlreadyRevealed_Errors(t *testing.T) {
	s := newClientRollTestServer(t)
	pr := newTestPendingRoll("campaign-1", protocol.ClientRollDie{ID: "d1", Sides: 20, Result: 11})

	if _, err := s.revealDie("prompt-1", pr, "d1"); err != nil {
		t.Fatalf("first revealDie() error = %v", err)
	}
	if _, err := s.revealDie("prompt-1", pr, "d1"); err == nil {
		t.Fatal("second revealDie() error = nil, want an error for an already-revealed die_id")
	}
}

func TestFinishRoll_IsIdempotent(t *testing.T) {
	s := newClientRollTestServer(t)
	pr := newTestPendingRoll("campaign-1", protocol.ClientRollDie{ID: "d1", Sides: 20, Result: 11})
	s.registerPendingRoll("prompt-1", pr)

	s.finishRoll("prompt-1", pr)
	if !pr.completed {
		t.Fatal("pr.completed = false after finishRoll, want true")
	}
	if _, ok := s.lookupPendingRoll("prompt-1"); ok {
		t.Error("pending roll still registered after finishRoll")
	}

	// A second call (a reveal racing the timeout, say) must not panic on
	// a double close(pr.done) or otherwise re-run the completion body.
	s.finishRoll("prompt-1", pr)

	select {
	case <-pr.done:
	default:
		t.Error("pr.done was never closed")
	}
}

func TestFinishRoll_SelfRevealsAnyStillHiddenDie(t *testing.T) {
	s := newClientRollTestServer(t)
	pr := newTestPendingRoll("campaign-1",
		protocol.ClientRollDie{ID: "d1", Sides: 20, Result: 11},
		protocol.ClientRollDie{ID: "d2", Sides: 20, Result: 6},
	)
	// d1 already revealed for real; d2 never was — the timeout path.
	pr.revealed["d1"] = true

	s.finishRoll("prompt-1", pr)

	if !pr.revealed["d2"] {
		t.Error("d2 not self-revealed by finishRoll")
	}
}

func TestCheckLabel_AllCheckTypeBranches(t *testing.T) {
	tests := []struct {
		name                      string
		checkType, ability, skill string
		want                      string
	}{
		{"AbilityCheckWithSkillAndAbility", "ability_check", "Charisma", "Persuasion", "Charisma (Persuasion) Check"},
		{"AbilityCheckWithAbilityOnly", "ability_check", "Strength", "", "Strength Check"},
		{"AbilityCheckWithSkillOnly", "ability_check", "", "Persuasion", "Persuasion Check"},
		{"AbilityCheckWithNeither", "ability_check", "", "", "Check"},
		{"SavingThrowWithAbility", "saving_throw", "Dexterity", "", "Dexterity Saving Throw"},
		{"SavingThrowWithoutAbility", "saving_throw", "", "", "Saving Throw"},
		{"DeathSave", "death_save", "", "", "Death Save"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkLabel(tt.checkType, tt.ability, tt.skill)
			if got != tt.want {
				t.Errorf("checkLabel(%q, %q, %q) = %q, want %q", tt.checkType, tt.ability, tt.skill, got, tt.want)
			}
		})
	}
}

func TestCheckPurpose_MapsEachCheckType(t *testing.T) {
	tests := []struct {
		checkType string
		want      protocol.ClientRollPurpose
	}{
		{"ability_check", protocol.ClientRollPurposeCheck},
		{"saving_throw", protocol.ClientRollPurposeSave},
		{"death_save", protocol.ClientRollPurposeDeathSave},
	}
	for _, tt := range tests {
		if got := checkPurpose(tt.checkType); got != tt.want {
			t.Errorf("checkPurpose(%q) = %q, want %q", tt.checkType, got, tt.want)
		}
	}
}

func TestCheckResultSummary_ReconstructsModifierAndCriticalSuffix(t *testing.T) {
	tests := []struct {
		name    string
		outcome *systemenginepb.Outcome
		want    string
	}{
		{
			name:    "PositiveModifier",
			outcome: &systemenginepb.Outcome{Total: 12, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 9}}},
			want:    "Charisma (Persuasion) Check: 9 +3 = 12",
		},
		{
			name:    "NegativeModifier",
			outcome: &systemenginepb.Outcome{Total: 12, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 13}}},
			want:    "Charisma (Persuasion) Check: 13 -1 = 12",
		},
		{
			name:    "CriticalSuccess",
			outcome: &systemenginepb.Outcome{Total: 23, CriticalSuccess: true, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 20}}},
			want:    "Charisma (Persuasion) Check: 20 +3 = 23 (Critical Success)",
		},
		{
			name:    "CriticalFailure",
			outcome: &systemenginepb.Outcome{Total: 4, CriticalFailure: true, Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: 1}}},
			want:    "Charisma (Persuasion) Check: 1 +3 = 4 (Critical Failure)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkResultSummary("Charisma (Persuasion) Check", tt.outcome)
			if got != tt.want {
				t.Errorf("checkResultSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
