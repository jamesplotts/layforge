// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// dmCharacterIntroSystemPrompt is runCharacterIntroPass's system prompt.
// This is deliberately its own narrow pass, not a reuse of
// dmNarrationSystemPrompt: there is no player action, mechanics
// transcript, or party roster yet (the character hasn't met anyone) —
// only the character's own data and, when bound, the campaign pack's
// real lore to ground a personal "how you got here" vignette in.
const dmCharacterIntroSystemPrompt = `You are the Dungeon Master for a tabletop RPG. A player has just finished creating their character, before the party has met. Write that character's own private opening scene — a short "how you got here" vignette grounded in their real data (race, class, background, name, gender when set), ending on a hook that's about to draw them toward the others, not a cliffhanger requiring a decision.

Let the character's real SRD background shape the scene, not a generic fantasy opener — a Criminal reads differently from a Sage, a Soldier, an Acolyte, a Folk Hero. If a campaign pack is bound, call list_locations first and place the scene somewhere real in it (ideally on the way toward the party's own eventual starting point, if one is given), and use list_npcs/list_encounters to tie in something already authored rather than invent a plot thread that might contradict the pack later. With no pack bound, stay grounded only in the character's own data — no invented place names or plot specifics that a pack bound afterward might contradict.

Write in second person ("you"), present tense, 3-5 sentences. This is shown only to this one player, privately — never mention or assume any other party member exists yet.`

// characterIntroPassMaxToolIterations bounds runCharacterIntroPass's
// tool-call loop — smaller than narrationPassMaxToolIterations since
// this pass only ever needs a location lookup or two before settling on
// text, never a multi-step mechanical resolution.
const characterIntroPassMaxToolIterations = 4

// characterIntroTimeout bounds sendCharacterIntro's own context —
// generous relative to how little work this pass actually does (at
// most a few lore lookups plus one narration), the same "the model
// might just be slow, not stuck" reasoning mechanicsPassTimeout/
// narrationPassTimeout document.
const characterIntroTimeout = 60 * time.Second

// sendCharacterIntro launches, in its own goroutine (mirroring
// runSlowPass's own detached-goroutine-with-panic-recovery shape — see
// its doc comment for why: the triggering request's own connection may
// already be gone by the time an LLM call finishes), a one-time,
// private DM narration introducing a freshly Approved character to
// their own player — backstory-flavored scene-setting and an adventure
// hook, grounded in the character's own real data and, when a campaign
// pack is bound, its real locations/NPCs/encounters, never a generic
// "you find yourself..." improvisation (see dmCharacterIntroSystemPrompt).
//
// Called exactly once per character, right when it becomes playable:
// sendCreationComplete (a quick/detailed roll finishing, or a claimed
// pregen) and concludeCharacterReview's own Approved case (an import
// that clears review) both call this. It is deliberately NOT called
// from the admin panel's own manual character-review-decision endpoint
// (internal/admin's handleReviewCharacter) — that package has no
// reference to this Server or its LLM/campaign-pack wiring today, so an
// import a Host approves by hand doesn't get this message yet. A
// documented gap, not a silent omission — the roll and claimed-pregen
// paths (how most characters are actually created) are unaffected.
//
// Best-effort throughout, matching every other narration-adjacent
// feature in this codebase: no LLM configured, no characters store, the
// character not found, or an LLM/tool failure just means this one
// player never gets the extra message — sendCreationComplete's own
// character.validation_result (or character.review_result) has already
// told them their character is ready either way, so nothing about
// actually playing depends on this succeeding.
func (s *Server) sendCharacterIntro(campaignID, characterID string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("recovered from panic sending character intro", "panic", r, "campaign_id", campaignID, "character_id", characterID)
		}
	}()
	if s.llm == nil || s.characters == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), characterIntroTimeout)
	defer cancel()

	character, err := s.campaignCharacter(ctx, campaignID, characterID)
	if err != nil {
		s.logger.Warn("character intro: could not fetch character, skipping", "error", err, "campaign_id", campaignID, "character_id", characterID)
		return
	}
	if character.OwnerID == "" {
		// No real player to send this to (shouldn't happen for a
		// creation-flow character, but characterIntroTools/callers never
		// guarantee OwnerID is set — fail closed, not to masterSenderID).
		return
	}

	text, ok := s.runCharacterIntroPass(ctx, campaignID, character)
	if !ok || text == "" {
		return
	}

	// Recorded with a private VisibilityScope (the same pattern
	// dmNarratePrivately/knowledge_scoping.go established) rather than
	// via sendClientDisplay's own non-empty-recipient path, so a
	// reconnecting player still finds this in log.history_response
	// instead of it existing only in the live moment they received it.
	msg, err := newMessage(campaignID, protocol.MessageTypeClientDisplay, protocol.ClientDisplayPayload{
		Text: text,
		Visibility: &protocol.VisibilityScope{
			Scope:                 protocol.VisibilityScopePrivate,
			VisibleToCharacterIDs: []string{characterID},
		},
	})
	if err != nil {
		s.logger.Warn("character intro: failed to build client.display message", "error", err, "campaign_id", campaignID, "character_id", characterID)
		return
	}
	recordEvent(ctx, s, msg)
	if err := sendToSender(s, character.OwnerID, msg); err != nil {
		s.logger.Warn("character intro: failed to deliver", "error", err, "campaign_id", campaignID, "character_id", characterID, "owner", character.OwnerID)
	}
}

// characterIntroTools returns the read-only campaign-pack lore lookups
// (list_locations/list_npcs/list_encounters) when a pack is bound, and
// nothing otherwise. The intro is pure narration — never a mechanical
// or state-changing tool — so unlike mechanicsTools/narrationTools it
// needs no systemEngine/characters gate, only "is there anything real
// to look up."
func (s *Server) characterIntroTools() []llm.Tool {
	if s.campaignPack == nil {
		return nil
	}
	return campaignPackLoreTools()
}

// runCharacterIntroPass runs a bounded tool-call loop — mirroring
// runNarrationPass's own shape (dm_slow_pass.go), offering only
// characterIntroTools() — ending once the model responds with plain
// text instead of a tool call. ok is false when the underlying LLM call
// failed or the pass never settled on narration within
// characterIntroPassMaxToolIterations.
//
// Tool calls here deliberately do NOT broadcast a tool.result the way
// runMechanicsPass/runNarrationPass's own calls do: those are logging a
// real DM turn for the whole table's transparency (design doc §8); this
// pass is Master's own private, pre-game content generation for one
// not-yet-seated player, and "list_locations succeeded" notes appearing
// in everyone's log while character creation is still in progress would
// be table-visible noise about a step nobody else is part of yet, not a
// transparency win.
func (s *Server) runCharacterIntroPass(ctx context.Context, campaignID string, character store.Character) (string, bool) {
	pol := s.campaignPolicy(ctx, campaignID)
	systemPrompt := withMaturityConstraint(dmCharacterIntroSystemPrompt, pol)

	userContent := fmt.Sprintf("Character data: %s\n", character.CharacterData)
	if s.campaignPack != nil {
		if loc := s.locationContextText(ctx, campaignID); loc != "" {
			userContent += "The party's own eventual starting point (once everyone converges) — " + loc
		}
	}

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userContent},
	}
	tools := s.characterIntroTools()

	for i := 0; i < characterIntroPassMaxToolIterations; i++ {
		resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
			Model:    s.narrativeModel,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			s.logger.Warn("character intro pass completion failed", "error", err, "campaign_id", campaignID, "character_id", character.ID)
			return "", false
		}
		if len(resp.ToolCalls) == 0 {
			return resp.Text, true
		}

		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			result, success, reasonCode := s.callDMTool(ctx, campaignID, masterSenderID, call)
			if !success {
				s.logger.Warn("character intro tool call failed", "campaign_id", campaignID, "character_id", character.ID, "tool", call.Name, "reason_code", reasonCode)
			}
			messages = append(messages, llm.Message{Role: llm.RoleTool, Content: result, ToolCallID: call.ID})
		}
	}

	s.logger.Warn("character intro pass ended without final narration", "campaign_id", campaignID, "character_id", character.ID, "max_iterations", characterIntroPassMaxToolIterations)
	return "", false
}
