// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// dmSlowPassSystemPrompt instructs the model for design doc §7's slow
// pass: narrate the DM/NPC reaction to a player's stated action, using
// design doc §8's tool-use pattern to resolve any mechanical uncertainty
// rather than inventing an outcome. Unlike narrativeFastPassSystemPrompt
// (which renders only what the player explicitly stated), this pass is
// where the model is trusted to decide what happens next — that's the
// whole reason it gets tool access and the fast pass doesn't.
const dmSlowPassSystemPrompt = `You are the Dungeon Master for a tabletop RPG session. Each message gives you the acting character's ID, their current character data (when available), and their stated action. Narrate the outcome in third-person, present-tense DM prose (2-4 sentences).

Rules:
- Always use the exact Character ID given to you for any tool call — never guess, invent, or shorten it.
- The character data given to you is the actual source of truth for what that character can currently do — check it before allowing something uncertain. A feature or action only works if it's actually listed; movement only works up to combatStats.speed (in feet) per turn without a stated, justified reason it doesn't apply. If the stated action isn't supported by the data you were given, narrate that it doesn't work as described (the character hesitates, fumbles, realizes they don't have that readied, etc.) rather than allowing it — you don't need a tool call for this, it's a narrative judgment grounded in the data given, not something to resolve_check your way around.
- Any spell's mechanical effect must go through cast_spell — never apply_effect, and never your own judgment about spellcasting.preparedSpellNames/knownSpellNames/slots. The engine checks whether it's actually prepared (or known) and whether a slot is available, and rejects the cast if not — don't narrate a cast as succeeding or failing until you've called cast_spell and seen the real result.
- If the action's outcome is uncertain or risky, call resolve_check before narrating the outcome — never invent a success or failure result.
- If a resolved check, or a clearly-stated non-spell action (e.g. drinking a healing potion), should change a character's hit points, call apply_effect — never invent a hit point change, and never use apply_effect for a spell's own damage/healing.
- Call get_character_status if you need to know a character's current condition before narrating a scene involving them.
- Every character or creature you resolve_check, apply_effect, get_character_status, or start_combat against must already have a real character ID — never invent one for a narrated monster/NPC. If you introduce a monster/NPC that needs mechanical presence, call get_character_schema first — every time, even if you think you already know the shape, since this engine's actual field names are not something to guess — then create_npc with a full character JSON matching exactly what it returned, and use the character_id it returns from then on — never the name you gave it narratively, and never a placeholder ID if create_npc failed.
- When a fight actually breaks out (not just narratively-described danger) and every combatant has a real character ID (create one with create_npc first if needed), call start_combat — this rolls real initiative and announces turn order. Once a character's turn is narratively over, call advance_turn — never decide or narrate whose turn is next yourself; Master computes it, skipping only the dead. An unconscious/dying character still gets a turn — Master automatically rolls their death save, you don't need to call anything for that. If start_combat or advance_turn fails, don't narrate as if it succeeded — acknowledge the fight is happening without formal turn order instead. Call end_combat once the fight is over.
- If generate_scene_image is available and a moment is genuinely worth illustrating (a striking new location, a dramatic reveal — not every beat), call it with a complete, self-contained visual description. It's slow and costly, so use it sparingly, and never claim an image was generated if the call fails. The image is shown to the table separately and automatically — never write a URL, a markdown image link, or any mention of "the image above" in your own narration text.
- Once you have everything you need, respond with narration only — no further tool calls, no meta-commentary, no quotation marks around it.`

// slowPassMaxToolIterations bounds the tool-call loop below — a
// misbehaving model that keeps calling tools instead of ever settling on
// narration must not hang this goroutine (or the campaign's dice
// tray/tool.result feed) forever. Was 5 until live testing against a
// stronger model (qwen3.8:27b, vs. the qwen2.5:32b this was originally
// tuned against) showed that cap bites a well-behaved model too: a
// single legitimate combat-start turn — get_character_schema,
// create_npc, start_combat, resolve_check, advance_turn — is exactly 5
// tool calls with zero iterations left to actually produce narration,
// so the whole turn silently dropped despite every mechanical step
// succeeding. Raised to give real multi-step sequences headroom while
// still bounding a genuinely runaway model.
const slowPassMaxToolIterations = 10

// slowPassTimeout bounds the whole slow pass, tool calls included — a
// context independent of the triggering connection's own ctx (see
// runSlowPass), since a player disconnecting mid-narration shouldn't cut
// off a DM reaction the rest of the table is still waiting to see.
const slowPassTimeout = 90 * time.Second

// runSlowPass runs design doc §7's slow pass for input: a multi-turn
// conversation with s.llm, offering it design doc §8's DM tools
// (dmTools), looping on any tool calls it makes (executing each via
// callDMTool, broadcasting a tool.result for each, and feeding the
// result back — design doc §8's call-logging requirement) until it
// responds with narration instead, then broadcasting that as
// narrative.dm_prose. Meant to be called via `go s.runSlowPass(...)` —
// see renderPlayerBubble — so it recovers its own panics rather than
// relying on handleConnection's recover, which only covers the
// triggering goroutine, not this detached one.
func (s *Server) runSlowPass(campaignID string, input protocol.NarrativePlayerInputMessage) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("recovered from panic in DM slow pass", "panic", r, "campaign_id", campaignID)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), slowPassTimeout)
	defer cancel()

	// The model has no other way to know which character_id to pass to a
	// tool call — Master doesn't feed it a full campaign roster yet, so
	// the acting character's ID has to ride along on the one turn it does
	// get. Caught by real end-to-end testing: without this, the model
	// guessed at an ID and every tool call failed with
	// character_not_found.
	userContent := fmt.Sprintf("Character ID: %s\n", input.Payload.CharacterID)

	// Feeding the acting character's own current data along with the ID
	// gives the model something real to judge feasibility against — a
	// feature not listed, movement past combatStats.speed — instead of
	// the ungrounded guess it was making before (this codebase had no
	// character-context mechanism at all until now; see
	// dmSlowPassSystemPrompt's matching instruction). Spell feasibility is
	// no longer judged from this data at all — cast_spell's hard gate
	// checks it instead — but the rest of character_data is still useful
	// context for the model's narration. Best-effort: a character not yet
	// found (a fresh stock-character race with character.upload, a bad
	// ID, characters disabled) just means the turn proceeds without this
	// section rather than failing outright — the model still has the ID
	// and action to work with, same as before this existed.
	if s.characters != nil {
		if character, err := s.campaignCharacter(ctx, campaignID, input.Payload.CharacterID); err != nil {
			s.logger.Warn("DM slow pass: could not fetch acting character's data, proceeding without it", "error", err, "campaign_id", campaignID, "character_id", input.Payload.CharacterID)
		} else {
			userContent += fmt.Sprintf("Character data: %s\n", character.CharacterData)
		}
	}
	// Same best-effort reasoning as the character-data section above:
	// a campaign with no pack bound (s.campaignPack nil, or nothing
	// bound for this campaign) just means the turn proceeds without a
	// location section, not a failure.
	if s.campaignPack != nil {
		userContent += s.locationContextText(ctx, campaignID)
	}
	userContent += fmt.Sprintf("Player action: %s", input.Payload.Text)
	systemPrompt := withMaturityConstraint(dmSlowPassSystemPrompt, s.campaignPolicy(ctx, campaignID))
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userContent},
	}

	// Tools require a real system engine to execute against — without
	// one, offering them would let the model "successfully" call a tool
	// that can only ever fail, which confuses a model more than simply
	// not offering tools at all. generate_scene_image has no such
	// dependency, so it's gated independently on s.imageGen.
	var tools []llm.Tool
	if s.systemEngine != nil && s.characters != nil {
		tools = dmTools()
	}
	if s.imageGen != nil {
		tools = append(tools, imageGenTool())
	}
	// Unlike imageGen, campaign-pack tools stay behind the same
	// system-engine gate as dmTools(): stash_item/stash_currency/
	// retrieve_item/retrieve_currency call real engine RPCs
	// (RemoveItemFromInventory/RemoveCurrency/AddItemToInventory/
	// AddCurrency), so a "no system engine, only campaignPack"
	// deployment would still get an inconsistent mix of working
	// (list_locations/list_npcs/list_encounters/travel_to/
	// claim_location) and always-failing tools — more confusing than
	// omitting the whole category, the same reasoning dmTools() itself
	// already applies wholesale.
	if s.systemEngine != nil && s.characters != nil && s.campaignPack != nil {
		tools = append(tools, campaignPackTools()...)
	}

	var finalText string
	// turnOrderCallFailed tracks whether a start_combat or advance_turn
	// call failed anywhere in this turn's tool loop — see the
	// looksLikeUnearnedTurnOrderClaim check below, which uses it to catch
	// a real, live-observed failure mode: the model calling start_combat,
	// having it fail (e.g. because an earlier create_npc for one of the
	// combatants also failed), and then narrating as if initiative had
	// been rolled anyway, directly contradicting both the tool result it
	// was just handed and this file's own system-prompt instruction not
	// to do that. Scoped to just these two tools (not every possible
	// failure) because they're the only ones whose success/failure
	// determines whether a real turn.state exists for players to be
	// mechanically gated by (see turn_order.go) — narrating past a failed
	// resolve_check or apply_effect is caught by other means (the model
	// has no result to narrate from in that case) or is a lower-stakes
	// flavor-text concern, not this specific mechanical contradiction.
	var turnOrderCallFailed bool
	// schemaFetched tracks whether get_character_schema has succeeded
	// earlier in THIS turn's tool loop — required before create_npc is
	// allowed to actually reach the system engine. Found necessary via
	// live testing: the system prompt already says to call
	// get_character_schema "if you don't already know the shape," and
	// against a real model the "already know" branch produced a
	// completely invented, non-OpenCombatEngine JSON document (generic
	// D&D fields like race/class/alignment, nested ability scores) that
	// failed validation every time — CLAUDE.md's "gates over prompting"
	// applied to schema knowledge specifically: the model reliably does
	// NOT already know this engine's real shape, so create_npc without a
	// schema fetch this turn is now rejected before it ever reaches
	// FromJson, rather than trusting the model's own judgment about
	// whether it needs to check. Deliberately per-turn, not persisted
	// across turns: each runSlowPass call is a fresh conversation with no
	// memory of an earlier turn's schema fetch (see this function's own
	// doc comment on why the character ID has to ride along every time
	// for the same reason), so "already fetched" can only ever mean
	// "already fetched in this exact reply."
	var schemaFetched bool
	for i := 0; i < slowPassMaxToolIterations; i++ {
		resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
			Model:    s.narrativeModel,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			s.logger.Warn("DM slow pass completion failed", "error", err, "campaign_id", campaignID)
			return
		}

		if len(resp.ToolCalls) == 0 {
			finalText = resp.Text
			break
		}

		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			if call.Name == "create_npc" && !schemaFetched {
				const reason = "create_npc rejected: you have not called get_character_schema in this reply yet. Call get_character_schema now, then retry create_npc with a character_json that matches its schema exactly — do not guess the shape."
				s.broadcastToolResult(ctx, campaignID, call.Name, false, "schema_not_fetched")
				messages = append(messages, llm.Message{Role: llm.RoleTool, Content: reason, ToolCallID: call.ID})
				continue
			}

			result, success, reasonCode := s.callDMTool(ctx, campaignID, input.SenderID, call)
			s.broadcastToolResult(ctx, campaignID, call.Name, success, reasonCode)
			if call.Name == "get_character_schema" && success {
				schemaFetched = true
			}
			if !success {
				// Diagnostic only (no behavior change) — added after live
				// testing turned up a resolve_check that failed with
				// character_not_found despite this turn's own context
				// supplying the correct ID: the call.Arguments the model
				// actually sent is otherwise never logged anywhere, so a
				// recurrence of that (or any other silent tool-call
				// failure) couldn't previously be root-caused after the
				// fact — only observed as a confusing table-side note.
				s.logger.Warn("DM tool call failed", "campaign_id", campaignID, "tool", call.Name, "reason_code", reasonCode, "arguments", string(call.Arguments))
				if call.Name == "start_combat" || call.Name == "advance_turn" {
					turnOrderCallFailed = true
				}
			}
			messages = append(messages, llm.Message{Role: llm.RoleTool, Content: result, ToolCallID: call.ID})
		}
	}

	if finalText == "" {
		s.logger.Warn("DM slow pass ended without final narration", "campaign_id", campaignID, "max_iterations", slowPassMaxToolIterations)
		return
	}
	if looksLikeMalformedToolCall(finalText) {
		// Observed live against a real Ollama server (qwen2.5:32b): the
		// model occasionally emits a failed tool-call attempt as plain
		// text — "<tool_call>\n{\"name\": ...}\n</tool_call>", sometimes
		// with the opening tag itself garbled — instead of populating the
		// structured ToolCalls field llm.OllamaProvider parses out of the
		// response. Broadcasting that verbatim to the whole table would
		// be exactly the kind of
		// ungated model output CLAUDE.md's "gates over prompting" rule
		// exists to prevent, so this counts as no usable narration this
		// turn, not a best-effort display of whatever the model produced.
		s.logger.Warn("DM slow pass produced a malformed tool-call artifact instead of narration; not broadcasting", "campaign_id", campaignID)
		return
	}
	if turnOrderCallFailed && looksLikeUnearnedTurnOrderClaim(finalText) {
		// Also observed live: exactly the sequence turnOrderCallFailed
		// documents above, followed by narration confidently announcing
		// "initiative is rolled" and who goes first — despite the
		// tool.result already broadcast to the table (via
		// broadcastToolResult above) showing start_combat failed. The
		// system prompt already tells the model not to do this; this is
		// the CLAUDE.md "gates over prompting" backstop for when it does
		// it anyway — same "no usable narration this turn" treatment as
		// looksLikeMalformedToolCall above, not a best-effort partial
		// broadcast, since there's no reliable way to strip just the false
		// claim out of otherwise-fine prose.
		s.logger.Warn("DM slow pass claimed turn order was established after start_combat/advance_turn failed; not broadcasting", "campaign_id", campaignID)
		return
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeNarrativeDmProse, protocol.NarrativeDmProsePayload{
		Text:               finalText,
		InReplyToMessageID: input.MessageID,
	})
	if err != nil {
		s.logger.Warn("failed to build narrative.dm_prose message", "error", err, "campaign_id", campaignID)
		return
	}
	recordEvent(ctx, s, msg)
	if err := broadcastMessage(s, msg); err != nil {
		s.logger.Warn("failed to broadcast narrative.dm_prose", "error", err, "campaign_id", campaignID)
	}
}

// broadcastToolResult announces one completed DM tool call to the whole
// campaign, for transparency (design doc §8) — every call is logged this
// way regardless of success, not just failures.
func (s *Server) broadcastToolResult(ctx context.Context, campaignID, toolName string, success bool, reasonCode string) {
	msg, err := newMessage(campaignID, protocol.MessageTypeToolResult, protocol.ToolResultPayload{
		ToolName:   toolName,
		Caller:     "dm",
		Success:    success,
		ReasonCode: reasonCode,
	})
	if err != nil {
		s.logger.Warn("failed to build tool.result message", "error", err, "campaign_id", campaignID)
		return
	}
	recordEvent(ctx, s, msg)
	if err := broadcastMessage(s, msg); err != nil {
		s.logger.Warn("failed to broadcast tool.result", "error", err, "campaign_id", campaignID)
	}
}

// looksLikeMalformedToolCall reports whether text is a failed tool-call
// attempt that leaked into plain narration instead of the model's
// structured tool-call field — see runSlowPass's call site for the real
// example this was written against. Deliberately loose (a false positive
// just means one DM turn goes silent instead of broadcasting garbage; a
// false negative means real narration slips through unfiltered) — there
// is no reliable way to parse an inconsistently-malformed tag, so this
// only needs to catch the common shapes, not every possible corruption.
func looksLikeMalformedToolCall(text string) bool {
	if strings.Contains(strings.ToLower(text), "tool_call") {
		return true
	}
	return strings.Contains(text, `"arguments"`) && strings.Contains(text, `"name"`)
}

// looksLikeUnearnedTurnOrderClaim reports whether text reads as narrating
// that structured turn order/initiative now exists — see runSlowPass's
// turnOrderCallFailed check, the only place this is called, for why that
// only matters when the start_combat/advance_turn call that would have
// actually established it is known to have failed. Deliberately loose,
// same trade-off looksLikeMalformedToolCall documents: a false positive
// costs one DM turn going silent instead of broadcasting a contradiction;
// a false negative lets real narration through. Keyword-based rather
// than trying to parse intent, on purpose — this only ever runs after
// turnOrderCallFailed is already true, so the keywords only need to catch
// the ways a model actually describes turn order/initiative, not
// distinguish combat narration in general (plenty of legitimate fight
// prose — "the goblin strikes first" as pure color, with no
// turnOrderCallFailed — never reaches this check at all).
func looksLikeUnearnedTurnOrderClaim(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{"initiative", "turn order", "whose turn", "goes first", "acts first"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
