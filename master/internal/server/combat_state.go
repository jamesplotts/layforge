// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"image/color"

	"github.com/jamesplotts/layforge/master/internal/combatmap"
)

// turnOrderSnapshot is turnOrder's serializable counterpart — turnOrder
// itself has unexported fields (deliberately: see its own doc comment on
// why it was in-memory-only to begin with), so persistence needs its own
// exported DTO to marshal.
type turnOrderSnapshot struct {
	Active       bool     `json:"active"`
	Order        []string `json:"order"`
	CurrentIndex int      `json:"current_index"`
	Round        int      `json:"round"`
}

// toSnapshot converts t to its serializable form, or nil if t is nil
// (no combat active for this campaign) — a nil-safe method so callers
// don't need their own "is there anything to persist" check before
// calling it.
func (t *turnOrder) toSnapshot() *turnOrderSnapshot {
	if t == nil {
		return nil
	}
	return &turnOrderSnapshot{
		Active:       t.active,
		Order:        append([]string(nil), t.order...),
		CurrentIndex: t.currentIndex,
		Round:        t.round,
	}
}

// toTurnOrder reverses toSnapshot, for rehydrating persisted state back
// into memory (WarmUpCombatState).
func (snap *turnOrderSnapshot) toTurnOrder() *turnOrder {
	if snap == nil {
		return nil
	}
	return &turnOrder{
		active:       snap.Active,
		order:        append([]string(nil), snap.Order...),
		currentIndex: snap.CurrentIndex,
		round:        snap.Round,
	}
}

// combatMapSnapshot is combatMapMeta's serializable counterpart, same
// reasoning as turnOrderSnapshot. combatmap.State and color.RGBA are
// both plain exported-field structs already, so no custom marshaling is
// needed for State or Colors beyond this wrapper.
type combatMapSnapshot struct {
	State  *combatmap.State       `json:"state"`
	Owners map[string]string      `json:"owners"`
	Colors map[string]color.RGBA  `json:"colors"`
}

func (m *combatMapMeta) toSnapshot() *combatMapSnapshot {
	if m == nil {
		return nil
	}
	return &combatMapSnapshot{State: m.state, Owners: m.owners, Colors: m.colors}
}

func (snap *combatMapSnapshot) toCombatMapMeta() *combatMapMeta {
	if snap == nil {
		return nil
	}
	return &combatMapMeta{state: snap.State, owners: snap.Owners, colors: snap.Colors}
}

// combatStateSnapshot is the one JSON blob persisted per campaign,
// covering both turn order and combat map together — they're already
// lifecycle-tied (endCombat clears both at once, dmGenerateCombatMap
// requires an active turn order to begin with), so one row is simpler
// than two separate ones.
type combatStateSnapshot struct {
	TurnOrder *turnOrderSnapshot `json:"turn_order,omitempty"`
	CombatMap *combatMapSnapshot `json:"combat_map,omitempty"`
}

// persistCombatState writes campaignID's current in-memory turn-order/
// combat-map state to storage, so a Master restart can rehydrate it
// (WarmUpCombatState) instead of losing it — see turnOrder's and
// combatMapMeta's own doc comments for the "lost on Master restart"
// limitation this closes. Best-effort: a failure here is logged, not
// propagated, the same posture every other non-critical persistence/
// broadcast call in this package already takes (e.g.
// broadcastTurnState) — losing this one write shouldn't block gameplay,
// only degrade what a restart could later recover.
func (s *Server) persistCombatState(ctx context.Context, campaignID string) {
	if s.combatState == nil {
		return
	}

	s.turnOrdersMu.Lock()
	turnSnap := s.turnOrders[campaignID].toSnapshot()
	s.turnOrdersMu.Unlock()

	s.combatMapsMu.Lock()
	mapSnap := s.combatMaps[campaignID].toSnapshot()
	s.combatMapsMu.Unlock()

	if turnSnap == nil && mapSnap == nil {
		return
	}

	payload, err := json.Marshal(combatStateSnapshot{TurnOrder: turnSnap, CombatMap: mapSnap})
	if err != nil {
		s.logger.Warn("failed to marshal combat state for persistence", "error", err, "campaign_id", campaignID)
		return
	}
	if err := s.combatState.SaveCombatState(ctx, campaignID, payload); err != nil {
		s.logger.Warn("failed to persist combat state", "error", err, "campaign_id", campaignID)
	}
}

// WarmUpCombatState rehydrates every campaign's persisted turn-order/
// combat-map state into memory. Intended to be called exactly once, at
// Master startup (main.go), before the WS listener starts accepting
// connections — every existing in-memory-map read site
// (combatParticipantIDs, currentTurnCharacterID, enforceTurnOrder,
// buildGridContext, ...) needs no changes at all as a result: the maps
// are already warm by the time any request touches them. A no-op
// (returns nil immediately) when no CombatStateStore was configured.
func (s *Server) WarmUpCombatState(ctx context.Context) error {
	if s.combatState == nil {
		return nil
	}

	campaignIDs, err := s.combatState.ListCombatStateCampaignIDs(ctx)
	if err != nil {
		return err
	}

	for _, campaignID := range campaignIDs {
		payload, ok, err := s.combatState.LoadCombatState(ctx, campaignID)
		if err != nil {
			s.logger.Warn("failed to load persisted combat state", "error", err, "campaign_id", campaignID)
			continue
		}
		if !ok {
			continue
		}

		var snap combatStateSnapshot
		if err := json.Unmarshal(payload, &snap); err != nil {
			s.logger.Warn("failed to parse persisted combat state", "error", err, "campaign_id", campaignID)
			continue
		}

		if turnState := snap.TurnOrder.toTurnOrder(); turnState != nil {
			s.turnOrdersMu.Lock()
			s.turnOrders[campaignID] = turnState
			s.turnOrdersMu.Unlock()
		}
		if mapState := snap.CombatMap.toCombatMapMeta(); mapState != nil {
			s.combatMapsMu.Lock()
			s.combatMaps[campaignID] = mapState
			s.combatMapsMu.Unlock()
		}
		s.logger.Info("rehydrated combat state", "campaign_id", campaignID)
	}
	return nil
}
