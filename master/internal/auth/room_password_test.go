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
		password   string
		wantOK     bool
	}{
		{name: "CorrectPassword_Authorized", campaignID: "protected-campaign", password: "hunter2", wantOK: true},
		{name: "WrongPassword_NotAuthorized", campaignID: "protected-campaign", password: "wrong", wantOK: false},
		{name: "EmptyPassword_NotAuthorized", campaignID: "protected-campaign", password: "", wantOK: false},
		{name: "UnconfiguredCampaign_OpenToAnyone", campaignID: "public-campaign", password: "", wantOK: true},
		{name: "UnconfiguredCampaign_AnyPasswordStillAuthorized", campaignID: "public-campaign", password: "anything", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.Authorize(context.Background(), tt.campaignID, auth.Credentials{CampaignPassword: tt.password})
			if err != nil {
				t.Fatalf("Authorize() error = %v, want nil", err)
			}
			if res.OK != tt.wantOK {
				t.Errorf("Authorize() ok = %v, want %v", res.OK, tt.wantOK)
			}
			if !res.OK && res.Reason == "" {
				t.Error("Authorize() reason is empty on a rejection, want an explanation")
			}
			if res.OK && res.Reason != "" {
				t.Errorf("Authorize() reason = %q on success, want empty", res.Reason)
			}
			if res.Identity.Authenticated() {
				t.Errorf("Authorize() asserted an identity %+v, want none from a room password", res.Identity)
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

	res, err := p.Authorize(context.Background(), "campaign-1", auth.Credentials{CampaignPassword: "secret"})
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
	if !res.OK {
		t.Error("Authorize(campaign-1, \"secret\") = false, want true (the original password should still work)")
	}

	res, err = p.Authorize(context.Background(), "campaign-1", auth.Credentials{CampaignPassword: "changed"})
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
	if res.OK {
		t.Error("Authorize(campaign-1, \"changed\") = true, want false (the post-construction mutation should not have taken effect)")
	}
}
