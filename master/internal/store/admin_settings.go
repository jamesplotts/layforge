// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store

import (
	"context"
	"time"
)

// CampaignSettings is one campaign's admin-panel-editable governance
// settings (design doc §3.3, §9.1, §9.5, §6.6) — the SQLite-backed
// counterpart to what main.go's -campaign-policies/-room-passwords flags
// load once from JSON at startup. Field names deliberately mirror
// policy.CampaignPolicy and auth.RoomPasswordProvider's own vocabulary;
// this package doesn't import either (see this file's package doc
// comment reasoning in store.go) so it stores their values as plain
// strings rather than those packages' own types.
type CampaignSettings struct {
	// PvPPolicy is one of policy.PvPPolicy's string values
	// (pve_only/pvp_allowed/pvp_with_consent), or empty for "not set by
	// the admin panel" — callers resolving a campaign's effective policy
	// treat that the same as no row existing at all (see
	// AdminSettingsStore.GetCampaignSettings's ok return).
	PvPPolicy string
	// PvPConsent lists player sender_ids who've pre-declared consent to
	// PvP (design doc §9.1) — only consulted when PvPPolicy is
	// pvp_with_consent.
	PvPConsent []string
	// MaturityTierPrompt and ImageMaturityTierPrompt mirror
	// policy.CampaignPolicy's own fields of the same name (design doc
	// §9.5, §6.3).
	MaturityTierPrompt      string
	ImageMaturityTierPrompt string
	// RoomPassword is the password required to join this campaign
	// (design doc §6.6); empty means no password is required. There is
	// no separate "is a password configured" flag — an empty string and
	// "never configured" are indistinguishable and behave identically
	// (open to anyone), matching auth.RoomPasswordProvider's existing
	// "not configured == open" semantics.
	RoomPassword string
}

// CampaignSummary is one row of the admin panel's real campaign list
// (design doc §3.3) — replacing the bare campaign_id list ListCampaignIDs
// alone provides, with enough for a host to actually recognize and manage
// a campaign: a human-chosen name, how many characters exist for it, when
// it was last actually played, and whether the host has archived it.
type CampaignSummary struct {
	CampaignID string
	// DisplayName is empty for a campaign never named via SaveCampaignMeta
	// — callers fall back to showing CampaignID itself, the same "absence
	// means show the raw identifier" pattern this package already uses
	// elsewhere.
	DisplayName string
	// PartyCount is how many characters exist for this campaign — not
	// how many distinct players, since a player can own more than one
	// character (design doc §9.4 has no account system yet to dedupe by).
	PartyCount int
	// LastActiveAt is the most recent event timestamp for this campaign,
	// falling back to the most recent character update when it has no
	// events yet (e.g. characters uploaded but no play started). The
	// zero time.Time means neither exists yet — a campaign the host just
	// created and named but nobody has touched.
	LastActiveAt time.Time
	// Archived is a purely cosmetic admin-panel display filter (see
	// SetCampaignArchived) — it never affects whether a campaign can
	// actually be joined or played over the WS endpoint.
	Archived bool
}

// AdminSettingsStore is Master's persistence for the admin panel's
// live-editable settings (design doc §3.3): per-campaign governance via
// CampaignSettings, plus process-level System-tab settings as a flat
// key/value map (keys: addr, llm_url, llm_model, system_engine_addr,
// comfyui_url, comfyui_workflow_path — see internal/admin's server for
// the authoritative key list). Implemented by SQLiteEventStore
// (admin_settings.go's SQL methods below) the same way CharacterStore and
// EventStore already are — one *sql.DB, several narrow interfaces.
type AdminSettingsStore interface {
	// GetCampaignSettings returns campaignID's stored settings. ok is
	// false (with a zero CampaignSettings) when nothing has ever been
	// saved for this campaign via the admin panel — callers should
	// treat that as "fall back to whatever else resolves this
	// campaign's policy," not as an error.
	GetCampaignSettings(ctx context.Context, campaignID string) (settings CampaignSettings, ok bool, err error)

	// SaveCampaignSettings upserts campaignID's settings, replacing any
	// previously stored row for it entirely (not a partial merge) —
	// callers that only want to change one field (e.g. just the room
	// password) must read the current settings first and write back the
	// full struct, the same read-modify-write pattern SaveCharacter's
	// callers already use.
	SaveCampaignSettings(ctx context.Context, campaignID string, settings CampaignSettings) error

	// ListCampaignIDs returns every campaign_id Master has ever seen —
	// the union of campaigns with events, characters, or admin-panel
	// settings — sorted for a stable admin-UI picker.
	ListCampaignIDs(ctx context.Context) ([]string, error)

	// ListCampaignSummaries returns the same set of campaigns
	// ListCampaignIDs does, plus display name/party size/last-active/
	// archived status for each — the admin panel's real campaign list
	// (design doc §3.3).
	ListCampaignSummaries(ctx context.Context) ([]CampaignSummary, error)

	// SaveCampaignMeta creates campaignID if it doesn't already have a
	// campaign_meta row (archived defaults false, created_at set to
	// now), or just updates its DisplayName if it does — archived/
	// archived_at are left untouched either way, so naming an already-
	// archived campaign doesn't silently unarchive it. This is the
	// admin panel's "create/name a campaign" action; it never touches
	// campaign_settings, characters, or the event log, and doesn't
	// change how campaignID is joined or played.
	SaveCampaignMeta(ctx context.Context, campaignID, displayName string) error

	// SetCampaignArchived upserts campaignID's archived flag — a purely
	// cosmetic admin-panel display filter (see CampaignSummary.Archived).
	// Creates a campaign_meta row (with an empty DisplayName) if
	// campaignID didn't already have one, the same as SaveCampaignMeta.
	SetCampaignArchived(ctx context.Context, campaignID string, archived bool) error

	// DeleteCampaign permanently removes every row for campaignID across
	// every table that references it — characters, events,
	// campaign_settings, campaign_meta, and combat_state — in one
	// transaction (all gone or none are). Fails with
	// ErrCampaignNotArchived, and deletes nothing, unless campaignID
	// already has a campaign_meta row with Archived = true: a real,
	// store-level precondition against permanently destroying a live
	// campaign's data, not something a caller can bypass by skipping the
	// admin UI's own archive-first flow.
	DeleteCampaign(ctx context.Context, campaignID string) error

	// GetSystemSettings returns every stored System-tab key/value pair.
	// A key never saved via the admin panel is simply absent from the
	// returned map — callers fall back to whatever seeded that key
	// (typically the CLI flag value) exactly as GetCampaignSettings'
	// callers fall back for an absent campaign.
	GetSystemSettings(ctx context.Context) (map[string]string, error)

	// SaveSystemSettings upserts each key/value pair in settings — keys
	// not present in settings are left untouched (a partial update,
	// unlike SaveCampaignSettings' full-replace), since System-tab keys
	// are independent process knobs, not one cohesive record.
	SaveSystemSettings(ctx context.Context, settings map[string]string) error
}
