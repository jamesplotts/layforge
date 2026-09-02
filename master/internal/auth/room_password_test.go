// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth_test

import (
	"context"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/auth"
)

func TestRoomPasswordProvider_Authorize(t *testing.T) {
	p := auth.NewRoomPasswordProvider(map[string]string{
		"protected-campaign": "hunter2",
	})

	tests := []struct {
		name       string
		campaignID string
		authToken  string
		wantOK     bool
	}{
		{name: "CorrectPassword_Authorized", campaignID: "protected-campaign", authToken: "hunter2", wantOK: true},
		{name: "WrongPassword_NotAuthorized", campaignID: "protected-campaign", authToken: "wrong", wantOK: false},
		{name: "EmptyToken_NotAuthorized", campaignID: "protected-campaign", authToken: "", wantOK: false},
		{name: "UnconfiguredCampaign_OpenToAnyone", campaignID: "public-campaign", authToken: "", wantOK: true},
		{name: "UnconfiguredCampaign_AnyTokenStillAuthorized", campaignID: "public-campaign", authToken: "anything", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason, err := p.Authorize(context.Background(), tt.campaignID, tt.authToken)
			if err != nil {
				t.Fatalf("Authorize() error = %v, want nil", err)
			}
			if ok != tt.wantOK {
				t.Errorf("Authorize() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && reason == "" {
				t.Error("Authorize() reason is empty on a rejection, want an explanation")
			}
			if ok && reason != "" {
				t.Errorf("Authorize() reason = %q on success, want empty", reason)
			}
		})
	}
}

func TestNewRoomPasswordProvider_DoesNotRetainCallersMap(t *testing.T) {
	// Adding a brand-new key to the caller's map after construction
	// can't distinguish "the provider retained the map" from "this
	// campaign was simply never configured" — both are open by design
	// (see the UnconfiguredCampaign cases above). So this test instead
	// mutates an *existing* entry's value, which the two scenarios do
	// disagree on: retained means the new value now works and the old
	// one doesn't; copied means neither of those hold.
	passwords := map[string]string{"campaign-1": "secret"}
	p := auth.NewRoomPasswordProvider(passwords)
	passwords["campaign-1"] = "changed"

	ok, _, err := p.Authorize(context.Background(), "campaign-1", "secret")
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
	if !ok {
		t.Error("Authorize(campaign-1, \"secret\") = false, want true (the original password should still work)")
	}

	ok, _, err = p.Authorize(context.Background(), "campaign-1", "changed")
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
	if ok {
		t.Error("Authorize(campaign-1, \"changed\") = true, want false (the post-construction mutation should not have taken effect)")
	}
}
