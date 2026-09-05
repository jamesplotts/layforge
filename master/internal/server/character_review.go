// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// characterReviewTimeout bounds runCharacterReviewPass's own LLM call —
// shorter than slowPassTimeout since this is a single-shot completion,
// never a multi-turn tool loop.
const characterReviewTimeout = 30 * time.Second

// characterReviewSystemPrompt instructs the DM AI's half of design doc
// §9.4's character-import veto: a real balance judgment call, not a
// mechanical fact (unlike the deterministic level-range check
// runCharacterReviewPass runs first) — so it's explicitly told to
// approve when in doubt, the same "don't invent a rejection" restraint
// this codebase's other narrative-judgment passes (spotlight balance,
// party roster) already lean on rather than a scoring formula.
const characterReviewSystemPrompt = `You are reviewing a player-submitted character sheet for admission into a tabletop RPG campaign. You are given the campaign's configured level range (if any) and the character's full mechanical data.

Call review_character exactly once with your verdict. Reject only for a genuine power-level or balance concern — ability scores far beyond what character creation could produce, equipment or resources wildly inappropriate for the stated level, or similar signs of a hand-edited or exploited sheet. Do not reject for flavor, name, backstory, or a build that is merely unusual but plausible. When in doubt, approve — the Host can always override your decision afterward.`

// reviewCharacterTool is the one tool offered to runCharacterReviewPass's
// completion call — deliberately not part of dmTools()/the slow-pass
// dispatch switch: this is a single-purpose call the DM model can't
// reach mid-narration, and its result is enforced directly by
// runCharacterReviewPass rather than going through callDMTool, since
// admitting a character isn't an in-play action.
func reviewCharacterTool() llm.Tool {
	return llm.Tool{
		Name:        "review_character",
		Description: "Report your verdict on whether this character should be admitted to the campaign.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"required": ["verdict", "reason"],
			"properties": {
				"verdict": {"type": "string", "enum": ["approve", "reject"]},
				"reason": {"type": "string", "description": "A short, human-readable explanation — shown to the player and to the Host."}
			}
		}`),
	}
}

// levelRangeText renders pol's configured level range as a short phrase
// for both the DM AI's own prompt context and a human-readable rejection
// reason — "no configured level range" when neither bound is set (see
// policy.CampaignPolicy.MinLevel/MaxLevel's own doc comment).
func levelRangeText(pol policy.CampaignPolicy) string {
	switch {
	case pol.MinLevel > 0 && pol.MaxLevel > 0:
		return fmt.Sprintf("%d-%d", pol.MinLevel, pol.MaxLevel)
	case pol.MinLevel > 0:
		return fmt.Sprintf("%d or higher", pol.MinLevel)
	case pol.MaxLevel > 0:
		return fmt.Sprintf("%d or lower", pol.MaxLevel)
	default:
		return "no configured level range"
	}
}

// runCharacterReviewPass is design doc §9.4's automatic post-upload
// review: a deterministic campaign level-range check first (no LLM
// involved — a pure mechanical fact doesn't need a model's opinion),
// then, if that passes and an LLM is configured, the DM AI's own balance
// judgment via reviewCharacterTool. Meant to be called via
// `go s.runCharacterReviewPass(...)` from importCharacter, right after a
// character is saved PendingReview — like runSlowPass, it recovers its
// own panics rather than relying on the triggering goroutine's recover.
//
// level is the FromJson response's own Actor.level (protocol/
// system_engine.proto) — a real top-level field, not something this
// function infers by parsing character_data itself (CLAUDE.md's "no
// D&D-shortcut inside Master" rule). Neither check running to a
// conclusion (no level range configured and no LLM available) leaves
// the character PendingReview — the Host's admin-panel review remains
// the only path to Approved in that case, never a silent auto-approve.
func (s *Server) runCharacterReviewPass(campaignID, characterID, senderID string, level int32) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("recovered from panic in character review pass", "panic", r, "campaign_id", campaignID, "character_id", characterID)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), characterReviewTimeout)
	defer cancel()

	pol := s.campaignPolicy(ctx, campaignID)
	if !pol.LevelInRange(int(level)) {
		reason := fmt.Sprintf("this campaign's level range is %s; this character is level %d", levelRangeText(pol), level)
		s.concludeCharacterReview(ctx, campaignID, characterID, senderID, store.CharacterStatusRejected, reason)
		return
	}

	if s.llm == nil {
		return
	}

	character, err := s.characters.GetCharacter(ctx, characterID)
	if err != nil {
		s.logger.Warn("character review pass: could not re-fetch character, leaving it pending review", "error", err, "character_id", characterID)
		return
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Model:        s.narrativeModel,
		SystemPrompt: characterReviewSystemPrompt,
		UserPrompt:   fmt.Sprintf("Campaign level range: %s\nCharacter data:\n%s", levelRangeText(pol), character.CharacterData),
		Tools:        []llm.Tool{reviewCharacterTool()},
	})
	if err != nil {
		s.logger.Warn("character review pass: LLM call failed, leaving character pending review", "error", err, "character_id", characterID)
		return
	}

	for _, call := range resp.ToolCalls {
		if call.Name != "review_character" {
			continue
		}
		var args struct {
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			s.logger.Warn("character review pass: malformed review_character arguments, leaving character pending review", "error", err, "character_id", characterID)
			return
		}
		var status store.CharacterStatus
		switch args.Verdict {
		case "approve":
			status = store.CharacterStatusApproved
		case "reject":
			status = store.CharacterStatusRejected
		default:
			s.logger.Warn("character review pass: unrecognized verdict, leaving character pending review", "verdict", args.Verdict, "character_id", characterID)
			return
		}
		s.concludeCharacterReview(ctx, campaignID, characterID, senderID, status, args.Reason)
		return
	}
	// The model responded without calling review_character at all — a
	// malformed/uncooperative completion, same safe-default reasoning as
	// every branch above: leave it PendingReview rather than guess.
	s.logger.Warn("character review pass: model did not call review_character, leaving character pending review", "character_id", characterID)
}

// concludeCharacterReview persists status for characterID and pushes
// character.review_result to senderID's own connection (sendToSender —
// never broadcast, this is a private outcome between Master and that
// one player, the same "no honest single slot in shared history for
// per-recipient content" reasoning map.token_state's own doc comment
// already gives — deliberately not recordEvent'd for that reason).
// Sending is best-effort: if senderID isn't currently connected, the
// status change still persists; they just find out on their next
// character.get/reconnect instead of live.
func (s *Server) concludeCharacterReview(ctx context.Context, campaignID, characterID, senderID string, status store.CharacterStatus, reason string) {
	character, err := s.characters.GetCharacter(ctx, characterID)
	if err != nil {
		s.logger.Warn("character review pass: could not re-fetch character to conclude review", "error", err, "character_id", characterID)
		return
	}
	character.Status = status
	character.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, character); err != nil {
		s.logger.Warn("character review pass: failed to save concluded status", "error", err, "character_id", characterID)
		return
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterReviewResult, protocol.CharacterReviewResultPayload{
		CharacterID: characterID,
		Status:      string(status),
		Reason:      reason,
	})
	if err != nil {
		s.logger.Warn("character review pass: failed to build character.review_result", "error", err, "character_id", characterID)
		return
	}
	if err := sendToSender(s, senderID, msg); err != nil {
		s.logger.Warn("character review pass: failed to send character.review_result", "error", err, "character_id", characterID)
	}
}
