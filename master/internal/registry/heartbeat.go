// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package registry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// npcOwnerSenderID mirrors package server's own unexported
// masterSenderID constant (internal/server/server.go) — the OwnerID
// create_npc saves every NPC character under. Duplicated here rather
// than shared/exported: this package deliberately doesn't import
// package server (that would invert the real dependency direction —
// main.go wires this package into server, not the reverse), and this
// is the only place outside package server that needs the same
// "exclude NPCs from a real-player count" check party_roster.go/
// spotlight.go already do internally.
const npcOwnerSenderID = "master"

// HeartbeatLoop periodically publishes each opted-in campaign's current
// state to a registry (package registry's own Client) and keeps it
// current — see Run/RunOnce. A campaign is only ever published when
// BOTH a registry is configured (main.go's -registry-url) AND that
// specific campaign's own store.CampaignSettings.RegistryListed is true
// with a non-empty JoinAddress — there is no "list everything" mode;
// nothing is ever sent anywhere about a campaign the host hasn't
// explicitly opted in.
type HeartbeatLoop struct {
	client            *Client
	adminStore        store.AdminSettingsStore
	characterStore    store.CharacterStore
	campaignPackStore store.CampaignPackStore
	logger            *slog.Logger

	// registrations tracks, per campaign_id, the id/token Register
	// returned — read and written only from RunOnce, which Run calls
	// strictly sequentially (one tick at a time, never overlapping), so
	// this needs no mutex.
	registrations map[string]registration
}

type registration struct {
	id    string
	token string
}

// NewHeartbeatLoop creates a HeartbeatLoop. adminStore/characterStore/
// campaignPackStore are the same store.SQLiteEventStore Master's own
// main.go already constructs for everything else — one concrete type
// satisfying several narrow interfaces, the same pattern server.New's
// own parameters already use.
func NewHeartbeatLoop(client *Client, adminStore store.AdminSettingsStore, characterStore store.CharacterStore, campaignPackStore store.CampaignPackStore, logger *slog.Logger) *HeartbeatLoop {
	return &HeartbeatLoop{
		client:            client,
		adminStore:        adminStore,
		characterStore:    characterStore,
		campaignPackStore: campaignPackStore,
		logger:            logger,
		registrations:     make(map[string]registration),
	}
}

// Run calls RunOnce immediately, then again every interval, until ctx
// is done. Meant to be started via `go loop.Run(ctx, interval)` from
// main.go, only when -registry-url is configured.
func (h *HeartbeatLoop) Run(ctx context.Context, interval time.Duration) {
	h.RunOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.RunOnce(ctx)
		}
	}
}

// RunOnce syncs every known campaign against the registry once: a
// newly-opted-in campaign is registered, an already-tracked one is
// heartbeated (re-registering transparently if the registry no longer
// recognizes it — ErrNotFound, see Client.Heartbeat's own doc comment),
// and a campaign that's no longer opted in (or was archived, or never
// existed at all) is deregistered if this process was tracking it.
// Exported (not just called from Run) so tests can drive individual
// sync rounds deterministically without waiting on a real ticker.
func (h *HeartbeatLoop) RunOnce(ctx context.Context) {
	summaries, err := h.adminStore.ListCampaignSummaries(ctx)
	if err != nil {
		h.logger.Warn("registry heartbeat: failed to list campaigns", "error", err)
		return
	}

	stillPresent := make(map[string]bool, len(summaries))
	for _, summary := range summaries {
		stillPresent[summary.CampaignID] = true
		h.syncCampaign(ctx, summary)
	}

	// A campaign this process was tracking that no longer shows up in
	// ListCampaignSummaries at all (a real edge case — nothing today
	// actually deletes a campaign's settings row) still shouldn't stay
	// published forever.
	for campaignID, reg := range h.registrations {
		if stillPresent[campaignID] {
			continue
		}
		if err := h.client.Deregister(ctx, reg.id, reg.token); err != nil {
			h.logger.Warn("registry heartbeat: failed to deregister a vanished campaign", "error", err, "campaign_id", campaignID)
		}
		delete(h.registrations, campaignID)
	}
}

func (h *HeartbeatLoop) syncCampaign(ctx context.Context, summary store.CampaignSummary) {
	settings, ok, err := h.adminStore.GetCampaignSettings(ctx, summary.CampaignID)
	if err != nil {
		h.logger.Warn("registry heartbeat: failed to load campaign settings", "error", err, "campaign_id", summary.CampaignID)
		return
	}
	shouldList := ok && settings.RegistryListed && settings.JoinAddress != "" && !summary.Archived

	reg, tracked := h.registrations[summary.CampaignID]

	if !shouldList {
		if tracked {
			if err := h.client.Deregister(ctx, reg.id, reg.token); err != nil {
				h.logger.Warn("registry heartbeat: failed to deregister", "error", err, "campaign_id", summary.CampaignID)
			}
			delete(h.registrations, summary.CampaignID)
		}
		return
	}

	listing := h.buildListing(ctx, summary, settings)

	if !tracked {
		id, token, err := h.client.Register(ctx, listing)
		if err != nil {
			h.logger.Warn("registry heartbeat: failed to register", "error", err, "campaign_id", summary.CampaignID)
			return
		}
		h.registrations[summary.CampaignID] = registration{id: id, token: token}
		return
	}

	err = h.client.Heartbeat(ctx, reg.id, reg.token, listing)
	if err == nil {
		return
	}
	if !errors.Is(err, ErrNotFound) {
		h.logger.Warn("registry heartbeat: failed to heartbeat", "error", err, "campaign_id", summary.CampaignID)
		return
	}
	// The registry no longer recognizes this listing (TTL expiry, or
	// the registry process itself restarted — its store is in-memory by
	// design) — register fresh rather than giving up on this campaign.
	id, token, err := h.client.Register(ctx, listing)
	if err != nil {
		h.logger.Warn("registry heartbeat: failed to re-register after 404", "error", err, "campaign_id", summary.CampaignID)
		return
	}
	h.registrations[summary.CampaignID] = registration{id: id, token: token}
}

// buildListing computes summary's current Listing entirely from data
// Master already has — no new computation beyond simple field mapping
// (see this package's own doc comment for the exact sources).
func (h *HeartbeatLoop) buildListing(ctx context.Context, summary store.CampaignSummary, settings store.CampaignSettings) Listing {
	adventureName := summary.DisplayName
	if binding, ok, err := h.campaignPackStore.GetCampaignPack(ctx, summary.CampaignID); err == nil && ok {
		if pack, err := campaignpack.LoadPack(binding.PackDir); err == nil && pack.Title != "" {
			adventureName = pack.Title
		}
	}
	if adventureName == "" {
		adventureName = summary.CampaignID
	}

	playersJoined := 0
	if characters, err := h.characterStore.ListCharacters(ctx, summary.CampaignID); err == nil {
		for _, c := range characters {
			if c.OwnerID != npcOwnerSenderID {
				playersJoined++
			}
		}
	} else {
		h.logger.Warn("registry heartbeat: failed to count players, reporting 0", "error", err, "campaign_id", summary.CampaignID)
	}

	return Listing{
		AdventureName:     adventureName,
		MinLevel:          settings.MinLevel,
		MaxLevel:          settings.MaxLevel,
		PlayersJoined:     playersJoined,
		PlayerSlots:       settings.MaxPlayers,
		PasswordProtected: settings.RoomPassword != "",
		JoinURL:           settings.JoinAddress,
		CampaignID:        summary.CampaignID,
	}
}
