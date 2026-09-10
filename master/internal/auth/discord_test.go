// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// fakeSessions is an auth.SessionLookuper backed by a map, plus a
// programmable error for the "provider can't perform the check" path.
type fakeSessions struct {
	byToken map[string]store.Account
	err     error
}

func (f fakeSessions) LookupOAuthSession(_ context.Context, token string) (store.OAuthSession, store.Account, error) {
	if f.err != nil {
		return store.OAuthSession{}, store.Account{}, f.err
	}
	acct, ok := f.byToken[token]
	if !ok {
		return store.OAuthSession{}, store.Account{}, store.ErrOAuthSessionNotFound
	}
	return store.OAuthSession{Token: token, AccountID: acct.ID}, acct, nil
}

func bramAccount() store.Account {
	return store.Account{ID: "discord:1", Provider: store.AuthProviderKindDiscord, ProviderUserID: "1", DisplayName: "Bram"}
}

// fakeAuthProvider is a canned auth.Provider for exercising the Next chain.
type fakeAuthProvider struct {
	ok     bool
	reason string
}

func (f fakeAuthProvider) Authorize(context.Context, string, auth.Credentials) (auth.Result, error) {
	return auth.Result{OK: f.ok, Reason: f.reason}, nil
}

func TestDiscordOAuthProvider_Authorize(t *testing.T) {
	sessions := fakeSessions{byToken: map[string]store.Account{"good": bramAccount()}}

	tests := []struct {
		name         string
		token        string
		next         auth.Provider
		wantOK       bool
		wantIdentity bool
	}{
		{name: "blank token", token: "", wantOK: false},
		{name: "unknown token", token: "nope", wantOK: false},
		{name: "valid token, no Next", token: "good", wantOK: true, wantIdentity: true},
		{
			name:         "valid token, Next accepts",
			token:        "good",
			next:         fakeAuthProvider{ok: true},
			wantOK:       true,
			wantIdentity: true,
		},
		{
			name:         "valid token, Next rejects",
			token:        "good",
			next:         fakeAuthProvider{ok: false, reason: "wrong room password"},
			wantOK:       false,
			wantIdentity: true, // identity still attached even on a Next rejection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := auth.NewDiscordOAuthProvider(sessions, tt.next)
			res, err := p.Authorize(context.Background(), "camp", auth.Credentials{AuthToken: tt.token})
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if res.OK != tt.wantOK {
				t.Errorf("Authorize() OK = %v, want %v", res.OK, tt.wantOK)
			}
			if !res.OK && res.Reason == "" {
				t.Error("Authorize() rejection has no Reason")
			}
			if got := res.Identity.Authenticated(); got != tt.wantIdentity {
				t.Errorf("Authorize() identity present = %v, want %v", got, tt.wantIdentity)
			}
			if tt.wantIdentity && res.Identity.DisplayName != "Bram" {
				t.Errorf("Authorize() identity = %+v, want Bram", res.Identity)
			}
		})
	}
}

func TestDiscordOAuthProvider_Authorize_ComposesWithRoomPassword(t *testing.T) {
	sessions := fakeSessions{byToken: map[string]store.Account{"good": bramAccount()}}
	roomPw := auth.NewRoomPasswordProvider(map[string]string{"camp": "hunter2"})
	p := auth.NewDiscordOAuthProvider(sessions, roomPw)

	// Valid Discord token but wrong campaign password → rejected, identity
	// still attached.
	res, err := p.Authorize(context.Background(), "camp",
		auth.Credentials{AuthToken: "good", CampaignPassword: "nope"})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if res.OK || !res.Identity.Authenticated() {
		t.Errorf("wrong password with valid login = %+v, want !OK with identity", res)
	}

	// Both correct → in.
	res, err = p.Authorize(context.Background(), "camp",
		auth.Credentials{AuthToken: "good", CampaignPassword: "hunter2"})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !res.OK || res.Identity.DisplayName != "Bram" {
		t.Errorf("login + password = %+v, want OK with Bram", res)
	}
}

func TestDiscordOAuthProvider_Authorize_ExpiredSession(t *testing.T) {
	p := auth.NewDiscordOAuthProvider(fakeSessions{err: store.ErrOAuthSessionExpired}, nil)
	res, err := p.Authorize(context.Background(), "camp", auth.Credentials{AuthToken: "stale"})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if res.OK || res.Reason == "" {
		t.Errorf("Authorize() = %+v, want a rejection with a reason", res)
	}
}

func TestDiscordOAuthProvider_Authorize_LookupError_IsError(t *testing.T) {
	sentinel := errors.New("db down")
	p := auth.NewDiscordOAuthProvider(fakeSessions{err: sentinel}, nil)
	if _, err := p.Authorize(context.Background(), "camp", auth.Credentials{AuthToken: "tok"}); !errors.Is(err, sentinel) {
		t.Fatalf("Authorize() error = %v, want it to wrap the lookup error", err)
	}
}
