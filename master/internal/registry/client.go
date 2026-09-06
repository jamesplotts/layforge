// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package registry is Master's opt-in client for the layforge.org
// public campaign directory (registry/, a separate standalone Go
// service in this same repo — see its own README for what it is).
// Master never publishes anything here unless -registry-url is
// configured *and* a specific campaign's own CampaignSettings.
// RegistryListed is true with a non-empty JoinAddress — see
// HeartbeatLoop's own doc comment for the actual per-campaign opt-in
// logic. A plain concrete Client, not an interface: unlike
// llm/imagegen/transcription's Provider interfaces, there is exactly
// one real registry implementation Master ever talks to (this repo's
// own registry/), so an interface here would be an abstraction with no
// second implementation.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound is returned by Heartbeat/Deregister when the registry no
// longer knows the given listing id — it expired past the registry's
// own TTL, or the registry process itself restarted (its store is
// in-memory, by design — see registry/internal/lobby's own doc
// comment). Callers (HeartbeatLoop) treat this as "register again from
// scratch," not a fatal error.
var ErrNotFound = errors.New("registry: listing not found")

// Listing is what Client sends the registry for a single campaign —
// field-for-field the same shape registry/internal/lobby.Fields
// expects, kept as this package's own type rather than importing that
// one directly: Master and the registry are deliberately separate Go
// modules with no shared dependency (see registry/'s own module
// separation rationale), so this is the wire contract, not a shared Go
// type.
type Listing struct {
	AdventureName     string
	MinLevel          int
	MaxLevel          int
	PlayersJoined     int
	PlayerSlots       int
	PasswordProtected bool
	JoinURL           string
	CampaignID        string
}

// Client calls a layforge.org-style registry's public API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client for the registry at baseURL (e.g.
// "https://layforge.org" or "http://192.168.1.56:8091"; no trailing
// slash).
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type listingWireBody struct {
	Token             string `json:"token,omitempty"`
	AdventureName     string `json:"adventure_name"`
	MinLevel          int    `json:"min_level"`
	MaxLevel          int    `json:"max_level"`
	PlayersJoined     int    `json:"players_joined"`
	PlayerSlots       int    `json:"player_slots"`
	PasswordProtected bool   `json:"password_protected"`
	JoinURL           string `json:"join_url"`
	CampaignID        string `json:"campaign_id"`
}

func wireBody(token string, l Listing) listingWireBody {
	return listingWireBody{
		Token:             token,
		AdventureName:     l.AdventureName,
		MinLevel:          l.MinLevel,
		MaxLevel:          l.MaxLevel,
		PlayersJoined:     l.PlayersJoined,
		PlayerSlots:       l.PlayerSlots,
		PasswordProtected: l.PasswordProtected,
		JoinURL:           l.JoinURL,
		CampaignID:        l.CampaignID,
	}
}

// Register creates a new listing, returning the id/token a later
// Heartbeat/Deregister call needs — see registry/internal/lobby.Store.
// Create's own doc comment for why the token can never be retrieved
// again after this call.
func (c *Client) Register(ctx context.Context, l Listing) (id, token string, err error) {
	body, err := json.Marshal(wireBody("", l))
	if err != nil {
		return "", "", fmt.Errorf("registry: marshaling listing: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/listings", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("registry: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("registry: calling registry at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("registry: register returned %s: %s", resp.Status, string(respBody))
	}
	var parsed struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", "", fmt.Errorf("registry: parsing register response: %w", err)
	}
	return parsed.ID, parsed.Token, nil
}

// Heartbeat updates an existing listing's fields and refreshes its
// TTL. Returns ErrNotFound if the registry has no record of id (see
// that error's own doc comment).
func (c *Client) Heartbeat(ctx context.Context, id, token string, l Listing) error {
	body, err := json.Marshal(wireBody(token, l))
	if err != nil {
		return fmt.Errorf("registry: marshaling listing: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/v1/listings/"+id, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("registry: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry: calling registry at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry: heartbeat returned %s: %s", resp.Status, string(respBody))
	}
	return nil
}

// Deregister removes a listing — a host's own graceful "stop listing
// this campaign" (opting out, or Master shutting down cleanly).
func (c *Client) Deregister(ctx context.Context, id, token string) error {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return fmt.Errorf("registry: marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/v1/listings/"+id, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("registry: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry: calling registry at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry: deregister returned %s: %s", resp.Status, string(respBody))
	}
	return nil
}
