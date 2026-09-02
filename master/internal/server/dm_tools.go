// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// dmTools returns the llm.Tool definitions offered to the DM model
// (design doc §8) — the System Engine RPCs already wired to
// player-facing dispatch (resolveCheck/applyCharacterEffect/
// sendCharacterState), reused here as tool implementations rather than
// duplicated, plus get_character_schema/create_npc (dmCreateNPC) so the
// DM can give a narrated monster/NPC a real mechanical record instead of
// inventing a character_id no other tool can look up — found necessary
// via live testing of start_combat before this existed (see
// turn_order.go's doc comment history / master/README.md). Design doc §8
// also names rules/SRD lookup, procedural generation, and campaign-notes
// retrieval as tool categories — none of those exist in this codebase
// (no RAG index, no procgen tables, no SRD lookup), so they're not
// stubbed out speculatively; only real, working tools are offered.
func dmTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "resolve_check",
			Description: "Resolve a mechanical check (ability check, saving throw, or death save) for a character in the current campaign. Always call this before narrating whether a risky or uncertain action succeeds — never guess or invent a result.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "check_type"],
				"properties": {
					"character_id": {"type": "string", "description": "The character's ID, from campaign context."},
					"check_type": {"type": "string", "enum": ["ability_check", "saving_throw", "death_save"]},
					"ability": {"type": "string", "description": "Required for ability_check/saving_throw — e.g. Strength, Dexterity."},
					"skill": {"type": "string", "description": "Optional skill name, for ability_check."}
				}
			}`),
		},
		{
			Name:        "apply_effect",
			Description: "Apply damage or healing to a character. Only call this after a resolve_check result justifies it (e.g. a failed save takes damage), or for a narratively-clear effect (e.g. drinking a healing potion) — never invent hit point changes without calling this.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "effect_type", "amount"],
				"properties": {
					"character_id": {"type": "string"},
					"effect_type": {"type": "string", "enum": ["damage", "heal"]},
					"amount": {"type": "integer"},
					"damage_type": {"type": "string", "description": "Optional, only for effect_type=damage."}
				}
			}`),
		},
		{
			Name:        "get_character_status",
			Description: "Get a character's current mechanical status (active, unconscious, dying, or dead) and full current data. Call this before narrating a scene involving a character whose condition you're unsure of.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id"],
				"properties": {
					"character_id": {"type": "string"}
				}
			}`),
		},
		{
			Name:        "start_combat",
			Description: "Start structured turn order for a fight. Rolls real initiative for each listed character (highest goes first) and announces whose turn it is. Call this once, when a fight actually begins — not for narratively-described danger with no mechanical turn order yet. Every character_id must be a real character already known to this campaign (e.g. from a player's own uploaded character) — never invent an ID for a narrated monster/NPC that has no real character record. If you don't have a real ID for every combatant, don't call this yet; just narrate the fight without structured turn order.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_ids"],
				"properties": {
					"character_ids": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Every character/creature ID taking part, in any order — initiative order is computed for you, never invent or assume it."
					}
				}
			}`),
		},
		{
			Name:        "advance_turn",
			Description: "End the current character's turn and move to the next one in initiative order, automatically skipping anyone unconscious, dying, or dead. Call this once a character's turn is narratively over — never decide or narrate whose turn is next yourself.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "end_combat",
			Description: "End structured turn order — call this once a fight is over (e.g. one side is defeated, flees, or negotiates).",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "get_character_schema",
			Description: "Get the JSON Schema this campaign's character data must conform to. Call this before create_npc if you don't already know the schema's shape — never guess field names.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "create_npc",
			Description: "Create a real character record for a monster or NPC you've narrated, from a full character JSON document matching get_character_schema's schema. Call this before referencing that monster/NPC in any other tool (resolve_check, apply_effect, start_combat, get_character_status) — those only work on characters that already exist, and inventing an ID for one that doesn't will always fail. Returns the new character's real ID; use that exact ID afterward, never the name you gave it narratively.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_json"],
				"properties": {
					"character_json": {"type": "string", "description": "The full character document as a JSON string, matching get_character_schema's schema exactly."}
				}
			}`),
		},
	}
}

// campaignCharacter looks up characterID and verifies it belongs to
// campaignID — the only gate a DM tool call gets today. Deliberately
// different from ownedCharacter: the DM legitimately acts on any
// character at the table, not just one a specific player owns, so there
// is no OwnerID check here. Design doc §8 says "governance gates (§9)
// are enforced at this layer" for DM tool calls — campaign-scoping is
// the one real gate this codebase has to enforce today (no PvP-policy or
// maturity-tier engine exists yet); this is a documented gap, not a
// silent omission.
func (s *Server) campaignCharacter(ctx context.Context, campaignID, characterID string) (store.Character, error) {
	character, err := s.characters.GetCharacter(ctx, characterID)
	if err != nil {
		return store.Character{}, fmt.Errorf("looking up character: %w", err)
	}
	if character.CampaignID != campaignID {
		return store.Character{}, fmt.Errorf("character %q does not belong to this campaign", characterID)
	}
	return character, nil
}

// callDMTool executes one tool call the DM model requested (design doc
// §8) against campaignID, returning a JSON string result to feed back to
// the model (see llm.Message's RoleTool), whether the call succeeded,
// and — on failure — a short machine-readable reason code for
// tool.result's ReasonCode (design doc §8's call-logging requirement).
func (s *Server) callDMTool(ctx context.Context, campaignID string, call llm.ToolCall) (result string, success bool, reasonCode string) {
	switch call.Name {
	case "resolve_check":
		return s.dmResolveCheck(ctx, campaignID, call.Arguments)
	case "apply_effect":
		return s.dmApplyEffect(ctx, campaignID, call.Arguments)
	case "get_character_status":
		return s.dmGetCharacterStatus(ctx, campaignID, call.Arguments)
	case "start_combat":
		return s.dmStartCombat(ctx, campaignID, call.Arguments)
	case "advance_turn":
		return s.dmAdvanceTurn(ctx, campaignID)
	case "end_combat":
		return s.dmEndCombat(ctx, campaignID)
	case "get_character_schema":
		return s.dmGetCharacterSchema(ctx)
	case "create_npc":
		return s.dmCreateNPC(ctx, campaignID, call.Arguments)
	default:
		return fmt.Sprintf("unknown tool %q", call.Name), false, "unknown_tool"
	}
}

func (s *Server) dmResolveCheck(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		CheckType   string `json:"check_type"`
		Ability     string `json:"ability"`
		Skill       string `json:"skill"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	paramFields := map[string]any{"checkType": args.CheckType}
	if args.Ability != "" {
		paramFields["ability"] = args.Ability
	}
	if args.Skill != "" {
		paramFields["skill"] = args.Skill
	}
	params, err := structpb.NewStruct(paramFields)
	if err != nil {
		return fmt.Sprintf("building params: %v", err), false, "internal_error"
	}

	resp, err := s.systemEngine.ResolveCheck(ctx, &systemenginepb.ResolveCheckRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Params:     params,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "resolution_failed"
	}

	// A DM-triggered check is just as much a shared table event as a
	// player-triggered roll.check_request — broadcast it the same way so
	// every client's dice tray animates it (design doc §3.1, §4).
	if err := s.broadcastRollOutcome(ctx, campaignID, character.ID, resp.Outcome); err != nil {
		s.logger.Warn("failed to broadcast DM-triggered roll outcome", "error", err, "character_id", character.ID)
	}

	rolls := make([]map[string]any, len(resp.Outcome.Rolls))
	for i, r := range resp.Outcome.Rolls {
		rolls[i] = map[string]any{"sides": r.Sides, "result": r.Result, "label": r.Label}
	}
	payload, err := json.Marshal(map[string]any{
		"total":            resp.Outcome.Total,
		"critical_success": resp.Outcome.CriticalSuccess,
		"critical_failure": resp.Outcome.CriticalFailure,
		"result_summary":   resp.Outcome.ResultSummary,
		"rolls":            rolls,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmApplyEffect(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		EffectType  string `json:"effect_type"`
		Amount      int    `json:"amount"`
		DamageType  string `json:"damage_type"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	effectFields := map[string]any{"effectType": args.EffectType, "amount": args.Amount}
	if args.DamageType != "" {
		effectFields["damageType"] = args.DamageType
	}
	effect, err := structpb.NewStruct(effectFields)
	if err != nil {
		return fmt.Sprintf("building effect: %v", err), false, "internal_error"
	}

	resp, err := s.systemEngine.ApplyEffect(ctx, &systemenginepb.ApplyEffectRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Effect:     effect,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "effect_failed"
	}

	newCharacterData, err := protojson.Marshal(resp.Actor.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated character data: %v", err), false, "internal_error"
	}
	character.CharacterData = newCharacterData
	character.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, character); err != nil {
		return fmt.Sprintf("saving updated character: %v", err), false, "internal_error"
	}

	status, statusErr := s.characterStatusAfter(ctx, resp.Actor)
	payload, err := json.Marshal(map[string]any{
		"applied": true,
		"status":  status,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	if statusErr != nil {
		s.logger.Warn("failed to fetch post-effect character status for DM tool result", "error", statusErr, "character_id", character.ID)
	}
	return string(payload), true, ""
}

func (s *Server) dmGetCharacterStatus(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	statusResp, err := s.systemEngine.GetCharacterStatus(ctx, &systemenginepb.GetCharacterStatusRequest{
		Actor: &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	status, ok := characterStatusString(statusResp.Status)
	if !ok {
		return "system engine returned an unrecognized character status", false, "engine_error"
	}

	payload, err := json.Marshal(map[string]any{
		"character_id":   character.ID,
		"status":         status,
		"character_data": json.RawMessage(character.CharacterData),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// characterStatusAfter fetches actor's current status, for callers that
// already have a fresh *systemenginepb.Actor (e.g. ApplyEffect's
// response) and just need the status string without a second character
// lookup. Returns "unknown" (not an error string in the JSON payload)
// alongside the error so a caller can still return a usable tool result
// even if this secondary call fails — losing the status shouldn't lose
// the fact that the effect itself was already successfully applied.
func (s *Server) characterStatusAfter(ctx context.Context, actor *systemenginepb.Actor) (string, error) {
	statusResp, err := s.systemEngine.GetCharacterStatus(ctx, &systemenginepb.GetCharacterStatusRequest{Actor: actor})
	if err != nil {
		return "unknown", err
	}
	status, ok := characterStatusString(statusResp.Status)
	if !ok {
		return "unknown", fmt.Errorf("system engine returned an unrecognized character status: %v", statusResp.Status)
	}
	return status, nil
}

// dmStartCombat, dmAdvanceTurn, and dmEndCombat are thin argument-
// unmarshaling wrappers around turn_order.go's startCombat/advanceTurn/
// endCombat — the actual turn-order bookkeeping lives there, not here,
// same split as dmResolveCheck delegating the roll itself to the system
// engine. A single reason code per tool ("start_combat_failed", etc.) is
// coarser than resolve_check's ("character_not_found" vs "engine_error"
// vs ...) — the underlying error's text still reaches the model as the
// tool result content either way, so nothing informative is lost, just
// the machine-readable code's granularity.

func (s *Server) dmStartCombat(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterIDs []string `json:"character_ids"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	state, err := s.startCombat(ctx, campaignID, args.CharacterIDs)
	if err != nil {
		return err.Error(), false, "start_combat_failed"
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmAdvanceTurn(ctx context.Context, campaignID string) (string, bool, string) {
	state, err := s.advanceTurn(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "advance_turn_failed"
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmEndCombat(ctx context.Context, campaignID string) (string, bool, string) {
	state, err := s.endCombat(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "end_combat_failed"
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmGetCharacterSchema mirrors sendCharacterSchema (the client-facing
// character.schema_request handler) as a DM tool, so the model can learn
// the shape create_npc's character_json must match instead of guessing
// field names — CLAUDE.md's "gates over prompting" cuts both ways here:
// Master never assumes the schema shape either (it stays engine-agnostic
// by forwarding whatever the engine reports), so the model has to ask.
func (s *Server) dmGetCharacterSchema(ctx context.Context) (string, bool, string) {
	resp, err := s.systemEngine.GetCharacterSchema(ctx, &systemenginepb.GetCharacterSchemaRequest{})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": resp.SchemaVersion,
		"json_schema":    json.RawMessage(resp.JsonSchema),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmCreateNPC gives a narrated monster/NPC a real store.Character record
// — the same FromJson + persist path character.upload uses for a
// player's own upload (see importCharacter in server.go), except OwnerID
// is masterSenderID rather than a player's sender_id, so ownedCharacter's
// per-player gate (roll.check_request, character.apply_effect,
// character.get) never matches it — only campaignCharacter's
// campaign-scoping gate does, which is exactly right: a player shouldn't
// be able to directly control a monster's character record, but the DM
// tools (which all go through campaignCharacter) should. Real end-to-end
// testing found this genuinely necessary: without it, start_combat could
// only ever include characters a player had already uploaded, since
// Master's own code must stay engine-agnostic (CLAUDE.md) and so cannot
// hardcode a stock NPC JSON shape the way the web client's
// uploadStockCharacter stopgap does for players.
func (s *Server) dmCreateNPC(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterJSON string `json:"character_json"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.CharacterJSON == "" {
		return "character_json is required", false, "invalid_arguments"
	}
	if s.characters == nil {
		return "character storage is disabled", false, "internal_error"
	}

	resp, err := s.systemEngine.FromJson(ctx, &systemenginepb.FromJsonRequest{Json: args.CharacterJSON})
	if err != nil {
		return fmt.Sprintf("calling system engine FromJson: %v", err), false, "engine_error"
	}
	if resp.Actor == nil {
		return npcCreationFailureMessage(resp.Warnings), false, "invalid_character"
	}
	for _, w := range resp.Warnings {
		if w.Severity == "error" {
			return npcCreationFailureMessage(resp.Warnings), false, "invalid_character"
		}
	}

	characterData, err := protojson.Marshal(resp.Actor.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling parsed character data: %v", err), false, "internal_error"
	}
	characterID, err := newCharacterID()
	if err != nil {
		return fmt.Sprintf("generating character id: %v", err), false, "internal_error"
	}
	now := time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, store.Character{
		ID:            characterID,
		CampaignID:    campaignID,
		OwnerID:       masterSenderID,
		SchemaVersion: resp.Actor.SchemaVersion,
		Status:        store.CharacterStatusPendingReview,
		CharacterData: characterData,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return fmt.Sprintf("saving character: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{
		"character_id":  characterID,
		"warning_count": len(resp.Warnings),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// npcCreationFailureMessage summarizes FromJson's validation warnings
// into readable text for the model to act on (e.g. fix a field and
// retry) — used both when FromJson couldn't parse an Actor at all and
// when it did but flagged an "error"-severity warning (design doc §9.4:
// "error" means the character shouldn't be importable as-is).
func npcCreationFailureMessage(warnings []*systemenginepb.ValidationWarning) string {
	if len(warnings) == 0 {
		return "character_json could not be parsed by the system engine"
	}
	parts := make([]string, len(warnings))
	for i, w := range warnings {
		parts[i] = fmt.Sprintf("%s: %s (%s)", w.FieldPath, w.Message, w.Severity)
	}
	return "character_json has validation problems: " + strings.Join(parts, "; ")
}
