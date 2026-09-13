// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// ClientRollTimeout bounds how long sendClientRollAndWait waits for the
// roller to reveal every die before Master self-reveals the rest and
// proceeds as if they had. Exported and reassignable — unlike
// mechanicsPassTimeout/narrationPassTimeout, which no test needs to
// actually elapse — specifically so a test exercising the self-reveal
// path can shrink it before constructing a Server, instead of a real
// test run waiting out a full production timeout. A test that changes
// it must restore it (t.Cleanup): it's shared package state, and this
// package's own tests run sequentially (no t.Parallel() in this suite).
var ClientRollTimeout = 25 * time.Second

// pendingRoll is one in-flight client.roll exchange: the dice the System
// Engine already resolved, waiting for the owning player to reveal them
// (or the timeout to self-reveal the rest). Guarded by
// Server.pendingRollsMu — same ephemeral, in-memory map+mutex shape as
// pendingPrompts/turnOrders/combatMaps/creationSessions, lost on a
// Master restart, same documented limitation those already accept.
type pendingRoll struct {
	campaignID string
	// ownerSender is character.OwnerID, captured once when the roll is
	// sent — client.roll_reveal must come from this same sender, the
	// same ownership test ownedCharacter itself uses elsewhere.
	ownerSender   string
	dice          []protocol.ClientRollDie // authoritative Results already set
	revealed      map[string]bool
	total         int
	resultSummary string
	done          chan struct{}
	completed     bool // guards finishRoll's body from running more than once
}

func (s *Server) registerPendingRoll(promptID string, pr *pendingRoll) {
	s.pendingRollsMu.Lock()
	defer s.pendingRollsMu.Unlock()
	s.pendingRolls[promptID] = pr
}

func (s *Server) lookupPendingRoll(promptID string) (*pendingRoll, bool) {
	s.pendingRollsMu.Lock()
	defer s.pendingRollsMu.Unlock()
	pr, ok := s.pendingRolls[promptID]
	return pr, ok
}

// checkLabel builds the human-readable heading resolve_check's
// check_type/ability/skill arguments imply — shown as the client.roll
// bubble's prompt text and the start of its result_summary line. No
// engine change needed: this is built entirely from the same arguments
// dmResolveCheck/resolveCheck already have in hand.
func checkLabel(checkType, ability, skill string) string {
	switch checkType {
	case "saving_throw":
		if ability != "" {
			return ability + " Saving Throw"
		}
		return "Saving Throw"
	case "death_save":
		return "Death Save"
	default: // "ability_check" and any future/unrecognized value
		switch {
		case ability != "" && skill != "":
			return fmt.Sprintf("%s (%s) Check", ability, skill)
		case skill != "":
			return skill + " Check"
		case ability != "":
			return ability + " Check"
		default:
			return "Check"
		}
	}
}

// checkPurpose maps resolve_check's check_type to the matching
// ClientRollPurpose the client uses to label/style the bubble.
func checkPurpose(checkType string) protocol.ClientRollPurpose {
	switch checkType {
	case "saving_throw":
		return protocol.ClientRollPurposeSave
	case "death_save":
		return protocol.ClientRollPurposeDeathSave
	default:
		return protocol.ClientRollPurposeCheck
	}
}

// checkResultSummary formats "<label>: <raw> <±modifier> = <total>",
// e.g. "Charisma (Persuasion) Check: 13 -1 = 12". The modifier needs no
// engine/proto change to recover: for a single-d20 roll (every
// ability_check/saving_throw/death_save resolves exactly one), total
// minus the die's own face value is exactly the modifier that was
// applied. Appends a critical success/failure suffix when the engine
// flagged one.
func checkResultSummary(label string, outcome *systemenginepb.Outcome) string {
	var raw int
	if len(outcome.Rolls) > 0 {
		raw = int(outcome.Rolls[0].Result)
	}
	total := int(outcome.Total)
	modifier := total - raw
	line := fmt.Sprintf("%s: %d %+d = %d", label, raw, modifier, total)
	switch {
	case outcome.CriticalSuccess:
		line += " (Critical Success)"
	case outcome.CriticalFailure:
		line += " (Critical Failure)"
	}
	return line
}

// sendClientRollAndWait sends character.OwnerID the interactive
// client.roll (dice + authoritative results already known — nothing
// about the actual roll changes, only whether it's revealed immediately
// or after a click), broadcasts client.roll_spectate (same dice, no
// results) to everyone else in campaignID, registers the pending roll,
// and blocks until every die is revealed — by a real client.roll_reveal
// (handleClientRollReveal), by ClientRollTimeout, or by ctx's own
// deadline, whichever comes first. The returned error only reports a
// real send/marshal failure, never "the player didn't click in time" —
// that's the designed self-reveal escape hatch (finishRoll), not a
// failure this returns.
//
// Safe to call inline from a caller already off the connection's own
// read-loop goroutine — dmResolveCheck and turn_order.go's automatic
// death-save path both run on the slow pass's own detached goroutine
// (see runSlowPass, started via `go` from renderPlayerBubble). NOT safe
// to call inline from a caller running on a connection's own read loop
// (resolveCheck) — that connection must stay free to read the very
// client.roll_reveal this call is waiting for; such a caller must invoke
// this via `go` instead, as resolveCheck itself does.
func (s *Server) sendClientRollAndWait(ctx context.Context, campaignID string, character store.Character, purpose protocol.ClientRollPurpose, label string, outcome *systemenginepb.Outcome) error {
	promptID, err := newRandomID()
	if err != nil {
		return err
	}

	dice := make([]protocol.ClientRollDie, len(outcome.Rolls))
	for i, r := range outcome.Rolls {
		dieID, err := newRandomID()
		if err != nil {
			return err
		}
		dice[i] = protocol.ClientRollDie{ID: dieID, Sides: int(r.Sides), Label: r.Label, Result: int(r.Result)}
	}

	rollMsg, err := newMessage(campaignID, protocol.MessageTypeClientRoll, protocol.ClientRollPayload{
		PromptID: promptID, CharacterID: character.ID, Purpose: purpose, Text: label, Dice: dice,
	})
	if err != nil {
		return err
	}
	if err := sendToSender(s, character.OwnerID, rollMsg); err != nil {
		return err
	}

	spectateDice := make([]protocol.ClientRollDie, len(dice))
	for i, d := range dice {
		// Result deliberately omitted (zero value) — anti-metagaming
		// (design doc §9.7): a spectator never learns the real result
		// until the roller reveals it themselves.
		spectateDice[i] = protocol.ClientRollDie{ID: d.ID, Sides: d.Sides, Label: d.Label}
	}
	spectateMsg, err := newMessage(campaignID, protocol.MessageTypeClientRollSpectate, protocol.ClientRollSpectatePayload{
		PromptID: promptID, CharacterID: character.ID, Purpose: purpose, Text: label, Dice: spectateDice,
	})
	if err != nil {
		return err
	}
	if err := broadcastMessageExceptSender(s, character.OwnerID, spectateMsg); err != nil {
		return err
	}

	pr := &pendingRoll{
		campaignID:    campaignID,
		ownerSender:   character.OwnerID,
		dice:          dice,
		revealed:      make(map[string]bool, len(dice)),
		total:         int(outcome.Total),
		resultSummary: checkResultSummary(label, outcome),
		done:          make(chan struct{}),
	}
	s.registerPendingRoll(promptID, pr)

	timer := time.NewTimer(ClientRollTimeout)
	defer timer.Stop()
	select {
	case <-pr.done:
	case <-timer.C:
		s.finishRoll(promptID, pr)
	case <-ctx.Done():
		s.finishRoll(promptID, pr)
	}
	return nil
}

// revealDie marks dieID revealed in pr, broadcasts a single
// client.roll_spectate_reveal for it, and reports how many dice are
// still hidden. Returns an error for an unknown or already-revealed
// dieID — handleClientRollReveal turns that into a system.error.
func (s *Server) revealDie(promptID string, pr *pendingRoll, dieID string) (remaining int, err error) {
	s.pendingRollsMu.Lock()
	if pr.revealed[dieID] {
		s.pendingRollsMu.Unlock()
		return 0, fmt.Errorf("die %q has already been revealed", dieID)
	}
	var result int
	found := false
	for i := range pr.dice {
		if pr.dice[i].ID == dieID {
			result = pr.dice[i].Result
			found = true
			break
		}
	}
	if !found {
		s.pendingRollsMu.Unlock()
		return 0, fmt.Errorf("no die %q in this roll", dieID)
	}
	pr.revealed[dieID] = true
	remaining = 0
	for _, d := range pr.dice {
		if !pr.revealed[d.ID] {
			remaining++
		}
	}
	s.pendingRollsMu.Unlock()

	if err := s.broadcastRollSpectateReveal(pr.campaignID, pr.ownerSender, promptID, dieID, result); err != nil {
		return remaining, err
	}
	return remaining, nil
}

func (s *Server) broadcastRollSpectateReveal(campaignID, excludeSender, promptID, dieID string, result int) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeClientRollSpectateReveal, protocol.ClientRollSpectateRevealPayload{
		PromptID: promptID, DieID: dieID, Result: result,
	})
	if err != nil {
		return err
	}
	return broadcastMessageExceptSender(s, excludeSender, msg)
}

// finishRoll is the single place a roll finishes, however it finishes:
// the last real reveal (handleClientRollReveal), a timeout, or the outer
// ctx ending first (sendClientRollAndWait). Self-reveals any die still
// hidden — the results were already decided when the roll was sent,
// there is nothing left to compute — broadcasts the remaining
// client.roll_spectate_reveal(s) plus client.roll_complete, removes the
// registry entry, and closes pr.done so sendClientRollAndWait's blocked
// caller resumes. pr.completed guards the body from running twice when
// a real reveal races the timeout.
func (s *Server) finishRoll(promptID string, pr *pendingRoll) {
	s.pendingRollsMu.Lock()
	if pr.completed {
		s.pendingRollsMu.Unlock()
		return
	}
	pr.completed = true
	var selfRevealed []protocol.ClientRollDie
	for i := range pr.dice {
		if !pr.revealed[pr.dice[i].ID] {
			pr.revealed[pr.dice[i].ID] = true
			selfRevealed = append(selfRevealed, pr.dice[i])
		}
	}
	delete(s.pendingRolls, promptID)
	s.pendingRollsMu.Unlock()

	for _, d := range selfRevealed {
		if err := s.broadcastRollSpectateReveal(pr.campaignID, pr.ownerSender, promptID, d.ID, d.Result); err != nil {
			s.logger.Warn("failed to self-reveal timed-out roll die", "error", err, "prompt_id", promptID, "die_id", d.ID)
		}
	}

	completeMsg, err := newMessage(pr.campaignID, protocol.MessageTypeClientRollComplete, protocol.ClientRollCompletePayload{
		PromptID: promptID, Total: pr.total, ResultSummary: pr.resultSummary,
	})
	if err != nil {
		s.logger.Warn("failed to build client.roll_complete", "error", err, "prompt_id", promptID)
	} else if err := broadcastMessage(s, completeMsg); err != nil {
		s.logger.Warn("failed to broadcast client.roll_complete", "error", err, "prompt_id", promptID)
	}
	close(pr.done)
}

// handleClientRollReveal is dispatch's entry point for
// client.roll_reveal: reject an unknown prompt_id, a sender who isn't
// this roll's own character.OwnerID, or an unknown/already-revealed
// die_id — all via sendError, mirroring deliverPromptAnswer's own
// real-rejection style. Otherwise reveals the die and finishes the roll
// once none remain hidden.
func (s *Server) handleClientRollReveal(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.ClientRollRevealMessage) error {
	promptID, dieID := req.Payload.PromptID, req.Payload.DieID
	if promptID == "" || dieID == "" {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("prompt_id and die_id are required"))
	}
	pr, ok := s.lookupPendingRoll(promptID)
	if !ok {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("no roll is waiting for that prompt_id (it may have already finished, or timed out)"))
	}
	if pr.ownerSender != senderID {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("that roll is not yours to reveal"))
	}
	remaining, err := s.revealDie(promptID, pr, dieID)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}
	if remaining == 0 {
		s.finishRoll(promptID, pr)
	}
	return nil
}

// broadcastMessageExceptSender is broadcastMessage's
// BroadcastExceptSender counterpart — same marshal-once-then-dispatch
// shape, for a message every client except one specific sender should
// receive (client.roll_spectate/client.roll_spectate_reveal).
func broadcastMessageExceptSender[T any](s *Server, excludeSender string, msg protocol.Message[T]) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", msg.Type, err)
	}
	s.hub.BroadcastExceptSender(msg.CampaignID, excludeSender, payload)
	return nil
}
