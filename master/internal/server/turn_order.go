// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// turnOrder is one campaign's live turn-order state machine (design doc
// §3.1, §9.3) — held in memory only, the same scope as session.Hub's
// connection registry, not persisted via store.EventStore/
// CharacterStore. A Master restart mid-combat loses it; the table would
// call start_combat again. Design doc §10 doesn't call out turn state as
// something that must survive a restart the way character data must, so
// this is a documented V1 limitation, not an oversight.
type turnOrder struct {
	active bool
	// order lists character IDs in initiative order, fixed once combat
	// starts — reordering mid-combat isn't SRD-typical and isn't
	// implemented.
	order        []string
	currentIndex int
	// round counts full trips through order, starting at 1 once combat
	// begins.
	round int
}

// toPayload converts to the wire representation. Returns a fresh copy of
// order each time so a caller can't mutate turnOrder's internal slice
// through the returned payload.
func (t turnOrder) toPayload() protocol.TurnStatePayload {
	payload := protocol.TurnStatePayload{Active: t.active, Round: t.round}
	if !t.active {
		return payload
	}
	payload.Order = append([]string(nil), t.order...)
	payload.CurrentCharacterID = t.order[t.currentIndex]
	return payload
}

// characterIsDead reports whether character's current mechanical status
// is exactly CHARACTER_STATUS_DEAD, per get_character_status (design doc
// §9.3) — the one status that removes a character from turn-order
// rotation entirely, used by both startCombat (excluding the dead from
// initiative) and advanceTurn (skipping them on their would-be turn).
// Unconscious/dying characters are deliberately NOT excluded here: real
// SRD play still gives them a turn — they roll a death saving throw
// instead of acting — which is exactly what startTurnFor's StartTurn
// call does automatically. Treating "not active" as "skip" (an earlier
// version of this function did) would mean an unconscious character
// never gets another turn once downed, so they'd never roll toward
// stabilizing or dying — stuck at 0 HP for the rest of the encounter.
func (s *Server) characterIsDead(ctx context.Context, character store.Character) (bool, error) {
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return false, fmt.Errorf("parsing stored character data for %q: %w", character.ID, err)
	}
	resp, err := s.systemEngine.GetCharacterStatus(ctx, &systemenginepb.GetCharacterStatusRequest{
		Actor: &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	})
	if err != nil {
		return false, fmt.Errorf("checking status for %q: %w", character.ID, err)
	}
	return resp.Status == systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD, nil
}

// startTurnFor runs the system engine's own start-of-turn bookkeeping
// for characterID (StartTurn, design doc §9.3) — most notably, an
// automatic death saving throw if the character is unconscious/dying,
// the SRD rule that this happens every turn without the DM having to
// remember it. Persists the character's updated state (action-economy
// reset, condition ticks, and any death-save mutation all live in the
// same character_data StartTurn returns) and broadcasts the death save
// as a real roll.request/roll.result (design doc §3.1, §4) if one was
// rolled, so the whole table sees it animate on the dice tray exactly
// like any other roll — never applied silently.
func (s *Server) startTurnFor(ctx context.Context, campaignID, characterID string) error {
	character, err := s.campaignCharacter(ctx, campaignID, characterID)
	if err != nil {
		return err
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Errorf("parsing stored character data for %q: %w", characterID, err)
	}

	resp, err := s.systemEngine.StartTurn(ctx, &systemenginepb.StartTurnRequest{
		RequestId:  "turn-start-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	})
	if err != nil {
		return fmt.Errorf("calling system engine StartTurn for %q: %w", characterID, err)
	}
	if !resp.Success {
		return fmt.Errorf("starting turn for %q: %s", characterID, resp.Error)
	}

	if resp.Actor != nil {
		newCharacterData, err := protojson.Marshal(resp.Actor.CharacterData)
		if err != nil {
			return fmt.Errorf("marshaling updated character data for %q: %w", characterID, err)
		}
		character.CharacterData = newCharacterData
		character.UpdatedAt = time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, character); err != nil {
			return fmt.Errorf("saving character %q after start-of-turn bookkeeping: %w", characterID, err)
		}
	}

	if resp.DeathSaveRolled && resp.DeathSaveOutcome != nil {
		if err := s.broadcastRollOutcome(ctx, campaignID, characterID, resp.DeathSaveOutcome); err != nil {
			s.logger.Warn("failed to broadcast automatic death save roll", "error", err, "campaign_id", campaignID, "character_id", characterID)
		}
	}

	return nil
}

// startCombat establishes initiative order for campaignID from
// characterIDs. Master rolls a real Dexterity ability check per
// character through the system engine — never trusts the DM model to
// invent or eyeball an order (CLAUDE.md's "gates over prompting") — then
// sorts descending by total. A character get_character_status already
// reports dead is silently excluded from initiative rather than failing
// the whole call, the same way advanceTurn would skip them on their
// turn anyway; unconscious/dying characters are included (see
// characterIsDead's doc comment for why). Replaces any turn order
// already running for campaignID (a fresh encounter starting
// mid-scene, e.g.).
func (s *Server) startCombat(ctx context.Context, campaignID string, characterIDs []string) (protocol.TurnStatePayload, error) {
	if len(characterIDs) == 0 {
		return protocol.TurnStatePayload{}, errors.New("start_combat requires at least one character_id")
	}

	type rolled struct {
		characterID string
		total       int32
	}
	var rolls []rolled
	for _, id := range characterIDs {
		character, err := s.campaignCharacter(ctx, campaignID, id)
		if err != nil {
			return protocol.TurnStatePayload{}, err
		}
		dead, err := s.characterIsDead(ctx, character)
		if err != nil {
			return protocol.TurnStatePayload{}, err
		}
		if dead {
			continue
		}

		characterData := &structpb.Struct{}
		if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
			return protocol.TurnStatePayload{}, fmt.Errorf("parsing stored character data for %q: %w", id, err)
		}
		params, err := structpb.NewStruct(map[string]any{"checkType": "ability_check", "ability": "Dexterity"})
		if err != nil {
			return protocol.TurnStatePayload{}, fmt.Errorf("building initiative params: %w", err)
		}
		resp, err := s.systemEngine.ResolveCheck(ctx, &systemenginepb.ResolveCheckRequest{
			RequestId:  "initiative-" + character.ID,
			CampaignId: campaignID,
			Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
			Params:     params,
		})
		if err != nil {
			return protocol.TurnStatePayload{}, fmt.Errorf("rolling initiative for %q: %w", id, err)
		}
		if !resp.Success {
			return protocol.TurnStatePayload{}, fmt.Errorf("rolling initiative for %q: %s", id, resp.Error)
		}
		// Initiative is as much a shared table event as any other check —
		// broadcast it the same way so every client's dice tray animates
		// it (design doc §3.1, §4).
		if err := s.broadcastRollOutcome(ctx, campaignID, character.ID, resp.Outcome); err != nil {
			s.logger.Warn("failed to broadcast initiative roll outcome", "error", err, "character_id", character.ID)
		}
		rolls = append(rolls, rolled{characterID: character.ID, total: resp.Outcome.Total})
	}

	if len(rolls) == 0 {
		return protocol.TurnStatePayload{}, errors.New("no non-dead characters to start combat with")
	}

	sort.SliceStable(rolls, func(i, j int) bool { return rolls[i].total > rolls[j].total })
	order := make([]string, len(rolls))
	for i, r := range rolls {
		order[i] = r.characterID
	}

	state := &turnOrder{active: true, order: order, currentIndex: 0, round: 1}
	s.turnOrdersMu.Lock()
	s.turnOrders[campaignID] = state
	s.turnOrdersMu.Unlock()

	// Combat starting also starts order[0]'s own turn.
	if err := s.startTurnFor(ctx, campaignID, order[0]); err != nil {
		s.logger.Warn("failed to run start-of-turn bookkeeping for the first character in initiative", "error", err, "campaign_id", campaignID, "character_id", order[0])
	}

	payload := state.toPayload()
	if err := s.broadcastTurnState(ctx, campaignID, payload); err != nil {
		s.logger.Warn("failed to broadcast turn.state", "error", err, "campaign_id", campaignID)
	}
	return payload, nil
}

// advanceTurn moves campaignID's turn order to the next character who
// isn't dead — design doc §9.3's requirement that this bookkeeping is
// mechanical, not left to the DM to remember. Landing on an unconscious/
// dying character still starts their turn (startTurnFor): SRD play has
// them roll a death saving throw instead of acting, which is exactly
// what that call does automatically — see characterIsDead's doc comment
// for why they aren't skipped the way a dead character is. If every
// character in order is dead, combat ends automatically: there is no
// one left who could take a turn.
func (s *Server) advanceTurn(ctx context.Context, campaignID string) (protocol.TurnStatePayload, error) {
	s.turnOrdersMu.Lock()
	state, ok := s.turnOrders[campaignID]
	s.turnOrdersMu.Unlock()
	if !ok || !state.active {
		return protocol.TurnStatePayload{}, errors.New("no combat is active for this campaign")
	}

	for step := 0; step < len(state.order); step++ {
		state.currentIndex++
		if state.currentIndex >= len(state.order) {
			state.currentIndex = 0
			state.round++
		}
		character, err := s.campaignCharacter(ctx, campaignID, state.order[state.currentIndex])
		if err != nil {
			return protocol.TurnStatePayload{}, err
		}
		dead, err := s.characterIsDead(ctx, character)
		if err != nil {
			return protocol.TurnStatePayload{}, err
		}
		if dead {
			continue
		}

		if err := s.startTurnFor(ctx, campaignID, character.ID); err != nil {
			s.logger.Warn("failed to run start-of-turn bookkeeping", "error", err, "campaign_id", campaignID, "character_id", character.ID)
		}

		payload := state.toPayload()
		if err := s.broadcastTurnState(ctx, campaignID, payload); err != nil {
			s.logger.Warn("failed to broadcast turn.state", "error", err, "campaign_id", campaignID)
		}
		return payload, nil
	}

	return s.endCombat(ctx, campaignID)
}

// endCombat clears campaignID's turn order and broadcasts the inactive
// turn.state. Safe to call even when no combat is active — the DM tool
// can simply narrate "the fight is over" without needing to track
// whether it already called this. Also clears any combat map generated
// for this encounter (combat_map.go) — a map is scoped to one active
// combat, the same lifecycle turnOrder itself has, not something that
// persists past the fight it was generated for.
func (s *Server) endCombat(ctx context.Context, campaignID string) (protocol.TurnStatePayload, error) {
	s.turnOrdersMu.Lock()
	delete(s.turnOrders, campaignID)
	s.turnOrdersMu.Unlock()

	s.combatMapsMu.Lock()
	delete(s.combatMaps, campaignID)
	s.combatMapsMu.Unlock()

	payload := protocol.TurnStatePayload{Active: false}
	if err := s.broadcastTurnState(ctx, campaignID, payload); err != nil {
		s.logger.Warn("failed to broadcast turn.state", "error", err, "campaign_id", campaignID)
	}
	return payload, nil
}

// broadcastTurnState announces payload to the whole campaign as
// turn.state, recording it to the durable event log first the same way
// every other Master-originated broadcast does (see broadcastToolResult,
// broadcastRollOutcome).
func (s *Server) broadcastTurnState(ctx context.Context, campaignID string, payload protocol.TurnStatePayload) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeTurnState, payload)
	if err != nil {
		return fmt.Errorf("building turn.state message: %w", err)
	}
	recordEvent(ctx, s, msg)
	return broadcastMessage(s, msg)
}

// currentTurnCharacterID reports whose turn it currently is for
// campaignID, and whether structured combat is active there at all. Safe
// to call from any goroutine — reads s.turnOrders under s.turnOrdersMu,
// the same lock startCombat/advanceTurn/endCombat use to mutate it.
func (s *Server) currentTurnCharacterID(campaignID string) (characterID string, active bool) {
	s.turnOrdersMu.Lock()
	defer s.turnOrdersMu.Unlock()
	state, ok := s.turnOrders[campaignID]
	if !ok || !state.active {
		return "", false
	}
	return state.order[state.currentIndex], true
}

// enforceTurnOrder rejects a player action on behalf of characterID when
// structured combat (design doc §3.1, §9.3) is active for campaignID and
// it is not currently that character's turn. A player acts freely
// outside combat, or on their own turn once it's active, but not out of
// turn once initiative has been rolled — used by resolveCheck and
// applyCharacterEffect (server.go), the two player-initiated mechanical
// actions.
//
// The DM's own tool calls (dm_tools.go) are deliberately NOT gated this
// way: the DM already narrates whose turn it is and calls advance_turn
// itself once a turn is over, so it's trusted with the same latitude a
// human GM has to call for a reaction or interrupt that doesn't strictly
// happen on the acting character's own turn — forcing the identical
// mechanical gate there would just block legitimate DM narration, not
// prevent a real abuse the way it does for a player spamming actions out
// of turn.
func (s *Server) enforceTurnOrder(campaignID, characterID string) error {
	current, active := s.currentTurnCharacterID(campaignID)
	if !active || characterID == current {
		return nil
	}
	return fmt.Errorf("it is not your turn — it is character %q's turn", current)
}
