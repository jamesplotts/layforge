// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func testAccount() store.Account {
	return store.Account{
		ID:             "discord:80351110224678912",
		Provider:       store.AuthProviderKindDiscord,
		ProviderUserID: "80351110224678912",
		DisplayName:    "Bram",
		AvatarURL:      "https://cdn.example/avatars/1.png",
	}
}

func TestUpsertAccount_InsertThenUpdate_RefreshesMutableFieldsKeepsCreatedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.UpsertAccount(ctx, testAccount())
	if err != nil {
		t.Fatalf("UpsertAccount() insert error = %v", err)
	}
	if first.CreatedAt.IsZero() || first.LastSeenAt.IsZero() {
		t.Fatalf("UpsertAccount() insert left timestamps zero: %+v", first)
	}

	renamed := testAccount()
	renamed.DisplayName = "Bram the Bold"
	renamed.AvatarURL = "https://cdn.example/avatars/2.png"
	renamed.LastSeenAt = first.CreatedAt.Add(time.Hour)

	second, err := s.UpsertAccount(ctx, renamed)
	if err != nil {
		t.Fatalf("UpsertAccount() update error = %v", err)
	}
	if second.DisplayName != "Bram the Bold" || second.AvatarURL != "https://cdn.example/avatars/2.png" {
		t.Errorf("UpsertAccount() update did not refresh mutable fields: %+v", second)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("UpsertAccount() update changed created_at: was %v, now %v", first.CreatedAt, second.CreatedAt)
	}
	if !second.LastSeenAt.After(first.LastSeenAt) {
		t.Errorf("UpsertAccount() update did not advance last_seen_at: %v -> %v", first.LastSeenAt, second.LastSeenAt)
	}
}

func TestUpsertAccount_InvalidProvider_Errors(t *testing.T) {
	s := newTestStore(t)
	bad := testAccount()
	bad.Provider = store.AuthProviderKindUnspecified
	if _, err := s.UpsertAccount(context.Background(), bad); err == nil {
		t.Fatal("UpsertAccount() with unspecified provider: want error, got nil")
	}
}

func TestGetAccount_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetAccount(context.Background(), "discord:nope"); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("GetAccount() unknown: error = %v, want ErrAccountNotFound", err)
	}
}

func TestOAuthSession_CreateLookupDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct, err := s.UpsertAccount(ctx, testAccount())
	if err != nil {
		t.Fatalf("UpsertAccount() error = %v", err)
	}

	const token = "deadbeefdeadbeefdeadbeefdeadbeef"
	if err := s.CreateOAuthSession(ctx, token, acct.ID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("CreateOAuthSession() error = %v", err)
	}

	sess, gotAcct, err := s.LookupOAuthSession(ctx, token)
	if err != nil {
		t.Fatalf("LookupOAuthSession() error = %v", err)
	}
	if sess.AccountID != acct.ID || gotAcct.ID != acct.ID || gotAcct.DisplayName != "Bram" {
		t.Errorf("LookupOAuthSession() = %+v / %+v, want account %q", sess, gotAcct, acct.ID)
	}

	if err := s.DeleteOAuthSession(ctx, token); err != nil {
		t.Fatalf("DeleteOAuthSession() error = %v", err)
	}
	if _, _, err := s.LookupOAuthSession(ctx, token); !errors.Is(err, store.ErrOAuthSessionNotFound) {
		t.Fatalf("LookupOAuthSession() after delete: error = %v, want ErrOAuthSessionNotFound", err)
	}
	// Deleting a missing token is a no-op, not an error.
	if err := s.DeleteOAuthSession(ctx, token); err != nil {
		t.Errorf("DeleteOAuthSession() on missing token: error = %v, want nil", err)
	}
}

func TestLookupOAuthSession_Expired_ReturnsExpiredThenCleansUp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct, _ := s.UpsertAccount(ctx, testAccount())

	// CreateOAuthSession refuses a past expiry, so insert a live session
	// then rely on lookup treating "now past ExpiresAt" as expired — write
	// a near-instant expiry and let it lapse.
	const token = "cafecafecafecafecafecafecafecafe"
	if err := s.CreateOAuthSession(ctx, token, acct.ID, time.Now().Add(20*time.Millisecond)); err != nil {
		t.Fatalf("CreateOAuthSession() error = %v", err)
	}
	time.Sleep(40 * time.Millisecond)

	if _, _, err := s.LookupOAuthSession(ctx, token); !errors.Is(err, store.ErrOAuthSessionExpired) {
		t.Fatalf("LookupOAuthSession() expired: error = %v, want ErrOAuthSessionExpired", err)
	}
	if _, _, err := s.LookupOAuthSession(ctx, token); !errors.Is(err, store.ErrOAuthSessionNotFound) {
		t.Fatalf("LookupOAuthSession() after expiry cleanup: error = %v, want ErrOAuthSessionNotFound", err)
	}
}

func TestCreateOAuthSession_PastExpiry_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct, _ := s.UpsertAccount(ctx, testAccount())
	if err := s.CreateOAuthSession(ctx, "t", acct.ID, time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("CreateOAuthSession() with past expiry: want error, got nil")
	}
}

func TestDeleteExpiredOAuthSessions_CountsOnlyExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct, _ := s.UpsertAccount(ctx, testAccount())

	if err := s.CreateOAuthSession(ctx, "live", acct.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateOAuthSession(live) error = %v", err)
	}
	if err := s.CreateOAuthSession(ctx, "soon", acct.ID, time.Now().Add(15*time.Millisecond)); err != nil {
		t.Fatalf("CreateOAuthSession(soon) error = %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	n, err := s.DeleteExpiredOAuthSessions(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteExpiredOAuthSessions() error = %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteExpiredOAuthSessions() = %d, want 1", n)
	}
	if _, _, err := s.LookupOAuthSession(ctx, "live"); err != nil {
		t.Errorf("live session lookup after cleanup: error = %v, want nil", err)
	}
}
