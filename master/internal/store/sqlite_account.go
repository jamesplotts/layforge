// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpsertAccount implements AccountStore.
func (s *SQLiteEventStore) UpsertAccount(ctx context.Context, account Account) (Account, error) {
	if !account.Provider.IsValid() {
		return Account{}, fmt.Errorf("store: upserting account: invalid provider %q", account.Provider)
	}
	if account.ProviderUserID == "" {
		return Account{}, errors.New("store: upserting account: provider_user_id is required")
	}
	if account.ID == "" {
		return Account{}, errors.New("store: upserting account: id is required")
	}

	now := account.LastSeenAt
	if now.IsZero() {
		now = time.Now()
	}
	created := account.CreatedAt
	if created.IsZero() {
		created = now
	}

	// ON CONFLICT on the (provider, provider_user_id) unique index rather
	// than the primary key: ID is derived from those two, so they move
	// together, but keying the upsert on the natural identity makes the
	// intent ("same person logging in again") explicit. created_at is left
	// untouched on conflict.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (account_id, provider, provider_user_id, display_name, avatar_url, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (provider, provider_user_id) DO UPDATE SET
			display_name = excluded.display_name,
			avatar_url   = excluded.avatar_url,
			last_seen_at = excluded.last_seen_at`,
		account.ID, string(account.Provider), account.ProviderUserID, account.DisplayName, account.AvatarURL,
		created.UTC().Format(occurredAtLayout), now.UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return Account{}, fmt.Errorf("store: upserting account: %w", err)
	}

	stored, err := s.getAccountBy(ctx, "provider = ? AND provider_user_id = ?", string(account.Provider), account.ProviderUserID)
	if err != nil {
		return Account{}, err
	}
	return stored, nil
}

// GetAccount implements AccountStore.
func (s *SQLiteEventStore) GetAccount(ctx context.Context, accountID string) (Account, error) {
	return s.getAccountBy(ctx, "account_id = ?", accountID)
}

// getAccountBy runs a single-row account query with the given WHERE
// clause tail and args, mapping sql.ErrNoRows to ErrAccountNotFound.
func (s *SQLiteEventStore) getAccountBy(ctx context.Context, where string, args ...any) (Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT account_id, provider, provider_user_id, display_name, avatar_url, created_at, last_seen_at
		 FROM accounts WHERE `+where,
		args...,
	)
	var a Account
	var provider, createdAt, lastSeenAt string
	err := row.Scan(&a.ID, &provider, &a.ProviderUserID, &a.DisplayName, &a.AvatarURL, &createdAt, &lastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("store: getting account: %w", err)
	}
	a.Provider = AuthProviderKind(provider)
	if a.CreatedAt, err = time.Parse(occurredAtLayout, createdAt); err != nil {
		return Account{}, fmt.Errorf("store: parsing account created_at: %w", err)
	}
	if a.LastSeenAt, err = time.Parse(occurredAtLayout, lastSeenAt); err != nil {
		return Account{}, fmt.Errorf("store: parsing account last_seen_at: %w", err)
	}
	return a, nil
}

// CreateOAuthSession implements AccountStore.
func (s *SQLiteEventStore) CreateOAuthSession(ctx context.Context, token, accountID string, expiresAt time.Time) error {
	if token == "" {
		return errors.New("store: creating oauth session: token is required")
	}
	if accountID == "" {
		return errors.New("store: creating oauth session: account_id is required")
	}
	now := time.Now()
	if !expiresAt.After(now) {
		return errors.New("store: creating oauth session: expires_at must be in the future")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_sessions (token, account_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, accountID, now.UTC().Format(occurredAtLayout), expiresAt.UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return fmt.Errorf("store: creating oauth session: %w", err)
	}
	return nil
}

// LookupOAuthSession implements AccountStore.
func (s *SQLiteEventStore) LookupOAuthSession(ctx context.Context, token string) (OAuthSession, Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT s.token, s.account_id, s.created_at, s.expires_at,
			a.account_id, a.provider, a.provider_user_id, a.display_name, a.avatar_url, a.created_at, a.last_seen_at
		 FROM oauth_sessions s
		 JOIN accounts a ON a.account_id = s.account_id
		 WHERE s.token = ?`,
		token,
	)
	var sess OAuthSession
	var acct Account
	var sCreated, sExpires, provider, aCreated, aLastSeen string
	err := row.Scan(
		&sess.Token, &sess.AccountID, &sCreated, &sExpires,
		&acct.ID, &provider, &acct.ProviderUserID, &acct.DisplayName, &acct.AvatarURL, &aCreated, &aLastSeen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthSession{}, Account{}, ErrOAuthSessionNotFound
	}
	if err != nil {
		return OAuthSession{}, Account{}, fmt.Errorf("store: looking up oauth session: %w", err)
	}
	if sess.CreatedAt, err = time.Parse(occurredAtLayout, sCreated); err != nil {
		return OAuthSession{}, Account{}, fmt.Errorf("store: parsing oauth session created_at: %w", err)
	}
	if sess.ExpiresAt, err = time.Parse(occurredAtLayout, sExpires); err != nil {
		return OAuthSession{}, Account{}, fmt.Errorf("store: parsing oauth session expires_at: %w", err)
	}
	if !sess.ExpiresAt.After(time.Now()) {
		// Best-effort cleanup so a stale token stops taking up space and a
		// re-lookup reports NotFound rather than Expired forever.
		_, _ = s.db.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE token = ?`, token)
		return OAuthSession{}, Account{}, ErrOAuthSessionExpired
	}

	acct.Provider = AuthProviderKind(provider)
	if acct.CreatedAt, err = time.Parse(occurredAtLayout, aCreated); err != nil {
		return OAuthSession{}, Account{}, fmt.Errorf("store: parsing account created_at: %w", err)
	}
	if acct.LastSeenAt, err = time.Parse(occurredAtLayout, aLastSeen); err != nil {
		return OAuthSession{}, Account{}, fmt.Errorf("store: parsing account last_seen_at: %w", err)
	}
	return sess, acct, nil
}

// DeleteOAuthSession implements AccountStore.
func (s *SQLiteEventStore) DeleteOAuthSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("store: deleting oauth session: %w", err)
	}
	return nil
}

// DeleteExpiredOAuthSessions implements AccountStore.
func (s *SQLiteEventStore) DeleteExpiredOAuthSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM oauth_sessions WHERE expires_at <= ?`,
		now.UTC().Format(occurredAtLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("store: deleting expired oauth sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
