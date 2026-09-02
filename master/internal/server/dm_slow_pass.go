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
const dmSlowPassSystemPrompt = `You are the Dungeon Master for a tabletop RPG session. Each message gives you the acting character's ID and their stated action. Narrate the outcome in third-person, present-tense DM prose (2-4 sentences).

Rules:
- Always use the exact Character ID given to you for any tool call — never guess, invent, or shorten it.
- If the action's outcome is uncertain or risky, call resolve_check before narrating the outcome — never invent a success or failure result.
- If a resolved check, or a clearly-stated action (e.g. drinking a healing potion), should change a character's hit points, call apply_effect — never invent a hit point change.
- Call get_character_status if you need to know a character's current condition before narrating a scene involving them.
- When a fight actually breaks out (not just narratively-described danger) AND you have a real character ID for every combatant, call start_combat — this rolls real initiative and announces turn order. Never invent a character ID for a narrated monster/NPC with no real record; if you don't have real IDs for everyone involved, just narrate the fight without calling start_combat rather than guessing an ID. Once a character's turn is narratively over, call advance_turn — never decide or narrate whose turn is next yourself, Master computes that and skips anyone unconscious, dying, or dead. If start_combat or advance_turn fails, don't narrate as if it succeeded — acknowledge the fight is happening without formal turn order instead. Call end_combat once the fight is over.
- Once you have everything you need, respond with narration only — no further tool calls, no meta-commentary, no quotation marks around it.`

// slowPassMaxToolIterations bounds the tool-call loop below — a
// misbehaving model that keeps calling tools instead of ever settling on
// narration must not hang this goroutine (or the campaign's dice
// tray/tool.result feed) forever.
const slowPassMaxToolIterations = 5

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
	// tool call — Master doesn't feed it a character roster or any other
	// campaign context yet (see this function's doc comment), so the
	// acting character's ID has to ride along on the one turn it does
	// get. Caught by real end-to-end testing: without this, the model
	// guessed at an ID and every tool call failed with
	// character_not_found.
	userContent := fmt.Sprintf("Character ID: %s\nPlayer action: %s", input.Payload.CharacterID, input.Payload.Text)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: dmSlowPassSystemPrompt},
		{Role: llm.RoleUser, Content: userContent},
	}

	// Tools require a real system engine to execute against — without
	// one, offering them would let the model "successfully" call a tool
	// that can only ever fail, which confuses a model more than simply
	// not offering tools at all.
	var tools []llm.Tool
	if s.systemEngine != nil && s.characters != nil {
		tools = dmTools()
	}

	var finalText string
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
			result, success, reasonCode := s.callDMTool(ctx, campaignID, call)
			s.broadcastToolResult(ctx, campaignID, call.Name, success, reasonCode)
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
