// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"errors"
	"time"
)

// AuthProviderKind names the identity provider an Account was created
// through. The zero value, AuthProviderKindUnspecified, is never a valid
// stored value — see IsValid. This mirrors the Unspecified-zero + IsValid
// enum-sentinel pattern used for CharacterStatus (design doc §12,
// CLAUDE.md): Go has no enum range to bound, so IsValid's switch is the
// range check.
type AuthProviderKind string

const (
	AuthProviderKindUnspecified AuthProviderKind = ""
	// AuthProviderKindDiscord is Discord OAuth2 (design doc §6.6's
	// reference provider). It is the only provider today; the type exists
	// so a second one (a bare room-code "account", another OAuth vendor)
	// doesn't require a schema change to tell them apart.
	AuthProviderKindDiscord AuthProviderKind = "discord"
)

// IsValid reports whether k is a recognized provider kind. It
// deliberately returns false for AuthProviderKindUnspecified.
func (k AuthProviderKind) IsValid() bool {
	switch k {
	case AuthProviderKindDiscord:
		return true
	default:
		return false
	}
}

// Account is one player's verified identity — the thing design doc §9.4
// says a character library is keyed to, distinct from the client-chosen
// character name that still travels in a message's sender_id. An account
// is created the first time a player completes the provider's login flow
// and refreshed (display name, avatar) on every subsequent login.
type Account struct {
	// ID is Master's own stable identifier for the account, of the form
	// "<provider>:<provider_user_id>" (e.g. "discord:80351110224678912").
	// Deriving it from the provider's own immutable user id — rather than
	// minting a random one — means the same person logging in again always
	// resolves to the same account without a lookup table.
	ID string

	// Provider is the identity provider this account authenticated
	// through.
	Provider AuthProviderKind

	// ProviderUserID is the provider's own immutable id for the user
	// (Discord's snowflake). Immutable even when the user renames
	// themselves, which DisplayName is not.
	ProviderUserID string

	// DisplayName is the user's current handle at the provider, refreshed
	// on each login. Shown to other players; never used as an identity
	// key (that's ID).
	DisplayName string

	// AvatarURL is a link to the user's provider avatar image, refreshed
	// on each login. May be empty. Carried now — ahead of any UI that
	// renders it — because backfilling it later would mean re-querying the
	// provider for every existing account.
	AvatarURL string

	CreatedAt  time.Time
	LastSeenAt time.Time
}

// OAuthSession is a Master-minted bearer token standing for a logged-in
// Account. The token itself is opaque random bytes (never a provider
// token, never a JWT); a client presents it as system.connect's
// auth_token. Sessions expire and are not refreshed — an expired one
// means the player logs in again, one click.
type OAuthSession struct {
	// Token is the opaque bearer value. Treated as a secret: it is never
	// logged in full and never serialized into a message another client
	// receives.
	Token string

	// AccountID is the Account.ID this session authenticates as.
	AccountID string

	CreatedAt time.Time
	ExpiresAt time.Time
}

// Errors returned by AccountStore implementations. Callers distinguishing
// failure reasons use errors.Is against these, not string comparison.
var (
	// ErrAccountNotFound is returned by GetAccount for an unknown id.
	ErrAccountNotFound = errors.New("store: account not found")
	// ErrOAuthSessionNotFound is returned by LookupOAuthSession when no
	// session with that token exists — including one that existed, expired,
	// and was cleaned up.
	ErrOAuthSessionNotFound = errors.New("store: oauth session not found")
	// ErrOAuthSessionExpired is returned by LookupOAuthSession when the
	// session exists but its ExpiresAt has passed. The row is deleted as a
	// side effect, so a subsequent lookup returns ErrOAuthSessionNotFound.
	ErrOAuthSessionExpired = errors.New("store: oauth session expired")
)

// AccountStore is Master's persistence for player accounts and their
// login sessions (design doc §6.6, §9.4). Like the other store
// interfaces it imports nothing from package protocol or package auth —
// it records and returns plain records, leaving the OAuth dance and the
// join-authorization decision to their own packages.
type AccountStore interface {
	// UpsertAccount inserts account, or — if an account with the same
	// (Provider, ProviderUserID) already exists — updates its
	// DisplayName, AvatarURL and LastSeenAt while preserving the original
	// CreatedAt and ID. It returns the stored record as it is after the
	// write. Fails if account.Provider is not IsValid or ProviderUserID
	// is empty.
	UpsertAccount(ctx context.Context, account Account) (Account, error)

	// GetAccount returns the account with the given id, or
	// ErrAccountNotFound.
	GetAccount(ctx context.Context, accountID string) (Account, error)

	// CreateOAuthSession records a new session token for accountID,
	// expiring at expiresAt. Fails if token or accountID is empty, or if
	// expiresAt is not in the future.
	CreateOAuthSession(ctx context.Context, token, accountID string, expiresAt time.Time) error

	// LookupOAuthSession resolves a session token to its session and the
	// account it belongs to. Returns ErrOAuthSessionNotFound for an
	// unknown token, or ErrOAuthSessionExpired (deleting the row) for one
	// past its expiry.
	LookupOAuthSession(ctx context.Context, token string) (OAuthSession, Account, error)

	// DeleteOAuthSession removes a session token (a logout). Deleting a
	// token that does not exist is not an error.
	DeleteOAuthSession(ctx context.Context, token string) error

	// DeleteExpiredOAuthSessions removes every session whose ExpiresAt is
	// at or before now, returning how many were deleted. Intended as
	// startup/periodic housekeeping — LookupOAuthSession already refuses
	// an expired session regardless of whether this has run.
	DeleteExpiredOAuthSessions(ctx context.Context, now time.Time) (int64, error)
}
