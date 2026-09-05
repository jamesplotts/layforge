// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"testing"

	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
)

func TestLevelRangeText(t *testing.T) {
	tests := []struct {
		name string
		pol  policy.CampaignPolicy
		want string
	}{
		{"Unconfigured", policy.CampaignPolicy{}, "no configured level range"},
		{"BothBounds", policy.CampaignPolicy{MinLevel: 1, MaxLevel: 5}, "1-5"},
		{"OnlyMin", policy.CampaignPolicy{MinLevel: 3}, "3 or higher"},
		{"OnlyMax", policy.CampaignPolicy{MaxLevel: 8}, "8 or lower"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelRangeText(tt.pol); got != tt.want {
				t.Errorf("levelRangeText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCharacterMayAct(t *testing.T) {
	tests := []struct {
		name      string
		character store.Character
		wantOK    bool
	}{
		{"PlayerOwned_Approved_MayAct", store.Character{OwnerID: "player-a", Status: store.CharacterStatusApproved}, true},
		{"PlayerOwned_PendingReview_MayNotAct", store.Character{OwnerID: "player-a", Status: store.CharacterStatusPendingReview}, false},
		{"PlayerOwned_Rejected_MayNotAct", store.Character{OwnerID: "player-a", Status: store.CharacterStatusRejected}, false},
		{"NPC_PendingReview_MayAct", store.Character{OwnerID: masterSenderID, Status: store.CharacterStatusPendingReview}, true},
		{"NPC_Approved_MayAct", store.Character{OwnerID: masterSenderID, Status: store.CharacterStatusApproved}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := characterMayAct(tt.character)
			if ok != tt.wantOK {
				t.Errorf("characterMayAct() ok = %v, want %v (reason = %q)", ok, tt.wantOK, reason)
			}
			if !ok && reason == "" {
				t.Error("characterMayAct() returned ok=false with an empty reason")
			}
		})
	}
}
