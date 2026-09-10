// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth

import (
	"context"
	"errors"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// SessionLookuper is the slice of store.AccountStore DiscordOAuthProvider
// needs: resolve a bearer token to its session and account. Declared at
// the point of consumption (CLAUDE.md's interface-segregation note) so
// the provider's tests can supply a fake without a whole store.
type SessionLookuper interface {
	LookupOAuthSession(ctx context.Context, token string) (store.OAuthSession, store.Account, error)
}

// DiscordOAuthProvider authorizes a join by validating a Master-minted
// session token (see DiscordOAuthHandler for how one is issued) and
// resolving it to a verified account. It is design doc §6.6's reference
// identity provider.
//
// It composes: when Next is non-nil, a valid token is necessary but not
// sufficient — Next (typically the room-password chain) still gets to
// accept or reject the join, and this provider only attaches the
// verified Identity to Next's Result. When Next is nil, a valid token is
// the whole check.
type DiscordOAuthProvider struct {
	sessions SessionLookuper
	// Next, if set, makes the final authorize decision for a
	// successfully-authenticated request; its Result's Identity is
	// replaced with this provider's verified one.
	Next Provider
}

var _ Provider = (*DiscordOAuthProvider)(nil)

// NewDiscordOAuthProvider creates a DiscordOAuthProvider backed by
// sessions, delegating the campaign-level decision to next (which may be
// nil, meaning a valid login is sufficient to join any campaign).
func NewDiscordOAuthProvider(sessions SessionLookuper, next Provider) *DiscordOAuthProvider {
	return &DiscordOAuthProvider{sessions: sessions, Next: next}
}

// Authorize implements Provider.
func (p *DiscordOAuthProvider) Authorize(ctx context.Context, campaignID string, creds Credentials) (Result, error) {
	if creds.AuthToken == "" {
		return Result{OK: false, Reason: "log in with Discord to join this campaign"}, nil
	}

	sess, account, err := p.sessions.LookupOAuthSession(ctx, creds.AuthToken)
	switch {
	case errors.Is(err, store.ErrOAuthSessionNotFound):
		return Result{OK: false, Reason: "your Discord login is not recognized — log in again"}, nil
	case errors.Is(err, store.ErrOAuthSessionExpired):
		return Result{OK: false, Reason: "your Discord login has expired — log in again"}, nil
	case err != nil:
		// The provider couldn't perform the check — a real error, not a
		// "not authorized" outcome.
		return Result{}, err
	}
	_ = sess // only the account is needed downstream today.

	identity := Identity{
		AccountID:   account.ID,
		DisplayName: account.DisplayName,
		AvatarURL:   account.AvatarURL,
	}

	if p.Next != nil {
		// Pass the whole creds so a room-password link still sees the
		// password; replace whatever Identity it returns with our
		// verified one.
		res, err := p.Next.Authorize(ctx, campaignID, creds)
		if err != nil {
			return Result{}, err
		}
		res.Identity = identity
		return res, nil
	}
	return Result{OK: true, Identity: identity}, nil
}
