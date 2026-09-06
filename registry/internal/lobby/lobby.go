// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package lobby implements the layforge.org public campaign directory's
// core, HTTP-free logic: an in-memory, TTL-based registry of currently-
// joinable Layforge campaigns. A self-hosted Master opts a campaign in
// (never automatic — see master/internal/registry's own doc comment)
// and periodically heartbeats it here; a public "open games" page reads
// the current list. Ephemeral by design, the same "lost on restart,
// self-healing on the next heartbeat" posture Master itself already
// uses for its own in-memory, TTL-adjacent state (turn order, audio
// stream buffers, character-creation sessions) — a restart here just
// means every listed campaign's Master gets a real 404 on its next
// heartbeat and transparently re-registers.
package lobby

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Listing is one campaign currently published to the directory.
type Listing struct {
	// ID identifies this listing for Heartbeat/Remove calls — assigned
	// by Create, opaque to callers.
	ID string
	// Token authorizes Heartbeat/Remove calls against this listing —
	// assigned by Create, returned only there. Never appears on a
	// Listing read back via Live, so a public listings read can never
	// leak it.
	Token string

	AdventureName string
	// MinLevel/MaxLevel are the campaign's configured level range —
	// mirrors policy.CampaignPolicy's own fields of the same name in
	// the Master codebase. 0 in either means unbounded in that
	// direction, the same convention.
	MinLevel int
	MaxLevel int
	// PlayersJoined is how many real player characters the campaign
	// currently has. PlayerSlots is the host's configured capacity —
	// 0 means unspecified/unlimited, not "zero slots."
	PlayersJoined int
	PlayerSlots   int
	// PasswordProtected reports whether the campaign requires a room
	// password to join — never the password itself, which this
	// directory never receives or stores.
	PasswordProtected bool
	// JoinURL is the Master WebSocket URL a player's own client needs
	// (design doc §4's join screen — "Master WebSocket URL"); CampaignID
	// is the second field that same join screen needs. Host-supplied,
	// since Master has no way to know its own externally-reachable
	// address.
	JoinURL    string
	CampaignID string

	// LastHeartbeat is when Create or Heartbeat last touched this
	// listing — Live/Sweep use it to decide whether a listing is still
	// current.
	LastHeartbeat time.Time
}

// Fields is the subset of Listing a caller supplies to Create or
// Heartbeat — everything except the server-assigned ID/Token and the
// server-computed LastHeartbeat.
type Fields struct {
	AdventureName     string
	MinLevel          int
	MaxLevel          int
	PlayersJoined     int
	PlayerSlots       int
	PasswordProtected bool
	JoinURL           string
	CampaignID        string
}

// Errors returned by Store's Heartbeat/Remove methods. Callers should
// use errors.Is against these rather than comparing error strings.
var (
	ErrNotFound      = errors.New("lobby: listing not found")
	ErrTokenMismatch = errors.New("lobby: token does not match")
)

// Store is an in-memory, mutex-guarded directory of Listings. The zero
// value is not usable — construct with NewStore.
type Store struct {
	mu       sync.Mutex
	listings map[string]*Listing
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{listings: make(map[string]*Listing)}
}

// Create adds a new listing from fields and returns its ID and Token —
// the Token is never retrievable again after this call returns, so a
// caller that loses it can only Remove the listing by creating a new
// one none of these means: it just becomes an orphaned entry that
// expires on its own once its host's own heartbeat loop gives up on it
// and moves on (see master/internal/registry's own re-register-on-404
// behavior, which is what actually happens in that case: a fresh
// Create, not a recovered Token).
func (s *Store) Create(fields Fields) (id, token string, err error) {
	id, err = newRandomID()
	if err != nil {
		return "", "", err
	}
	token, err = newRandomID()
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.listings[id] = &Listing{
		ID:                id,
		Token:             token,
		AdventureName:     fields.AdventureName,
		MinLevel:          fields.MinLevel,
		MaxLevel:          fields.MaxLevel,
		PlayersJoined:     fields.PlayersJoined,
		PlayerSlots:       fields.PlayerSlots,
		PasswordProtected: fields.PasswordProtected,
		JoinURL:           fields.JoinURL,
		CampaignID:        fields.CampaignID,
		LastHeartbeat:     time.Now().UTC(),
	}
	return id, token, nil
}

// Heartbeat updates id's fields (a campaign's player count/level range/
// etc. can change between heartbeats, so every field is resent and
// applied, not just a "still alive" ping) and refreshes its
// LastHeartbeat. Returns ErrNotFound if id doesn't exist (expired via
// Sweep, or never existed — e.g. after a Store restart), or
// ErrTokenMismatch if token doesn't match the one Create returned for
// id.
func (s *Store) Heartbeat(id, token string, fields Fields) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.listings[id]
	if !ok {
		return ErrNotFound
	}
	if l.Token != token {
		return ErrTokenMismatch
	}
	l.AdventureName = fields.AdventureName
	l.MinLevel = fields.MinLevel
	l.MaxLevel = fields.MaxLevel
	l.PlayersJoined = fields.PlayersJoined
	l.PlayerSlots = fields.PlayerSlots
	l.PasswordProtected = fields.PasswordProtected
	l.JoinURL = fields.JoinURL
	l.CampaignID = fields.CampaignID
	l.LastHeartbeat = time.Now().UTC()
	return nil
}

// Remove deletes id — a host's own graceful "stop listing this
// campaign" (opting out, or shutting Master down cleanly), as opposed
// to just letting it expire via Sweep. Returns ErrNotFound or
// ErrTokenMismatch under the same conditions as Heartbeat; a rejected
// call never deletes anything.
func (s *Store) Remove(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.listings[id]
	if !ok {
		return ErrNotFound
	}
	if l.Token != token {
		return ErrTokenMismatch
	}
	delete(s.listings, id)
	return nil
}

// Live returns every listing whose LastHeartbeat is within ttl of now,
// in no particular order, with Token always zeroed — the public "open
// games" read never needs or should see it. A listing past ttl is
// filtered out here without being deleted; Sweep is what actually
// reclaims the map's memory for stale entries.
func (s *Store) Live(ttl time.Duration) []Listing {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ttl)
	result := make([]Listing, 0, len(s.listings))
	for _, l := range s.listings {
		if l.LastHeartbeat.Before(cutoff) {
			continue
		}
		listingCopy := *l
		listingCopy.Token = ""
		result = append(result, listingCopy)
	}
	return result
}

// Sweep deletes every listing whose LastHeartbeat is more than ttl in
// the past — meant to be called periodically (main.go) so a long-
// running Store doesn't accumulate abandoned entries forever; Live
// already excludes them from reads regardless of whether Sweep has run
// yet.
func (s *Store) Sweep(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ttl)
	for id, l := range s.listings {
		if l.LastHeartbeat.Before(cutoff) {
			delete(s.listings, id)
		}
	}
}

// newRandomID returns a random, sufficiently-unique hex-encoded id —
// used for both Listing.ID and Listing.Token, matching master's own
// internal/server/id.go convention.
func newRandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("lobby: generating random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
