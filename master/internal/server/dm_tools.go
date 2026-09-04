// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
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
			Description: "Apply damage or healing to a character. Only call this after a resolve_check result justifies it (e.g. a failed save takes damage), or for a narratively-clear effect (e.g. drinking a healing potion) — never invent hit point changes without calling this. Never use this for a spell's own damage/healing — call cast_spell for that instead, which checks whether the spell is actually prepared/known and has an available slot before anything happens. Damaging a different player's own character (as opposed to a monster/NPC) is subject to this campaign's PvP policy and may be rejected.",
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
			Name:        "generate_combat_map",
			Description: "Generate a tactical grid map and place every combatant's token on it for the currently active fight — call this only when the physical space genuinely matters (a real dungeon room, not an abstract narrated skirmish), not for every combat automatically, the same restraint generate_scene_image uses. Requires start_combat to have already been called for this campaign. Each player only ever sees what their own character can currently see (fog of war) — this is purely tracking/display, it doesn't change what apply_effect/cast_spell/resolve_check do.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "get_character_schema",
			Description: "Get the JSON Schema this campaign's character data must conform to. Call this before create_npc if you don't already know the schema's shape — never guess field names.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "cast_spell",
			Description: "Cast a character's spell — the only correct way to resolve a spell's mechanical effect. The engine checks whether the spell is actually prepared (or known, for a caster who doesn't prepare spells) and whether a slot is available, and rejects the cast if not — never call apply_effect for a spell instead, and never narrate a cast as succeeding or failing until you've called this and seen the real result. Omit target_character_id for a self-only spell (e.g. a buff on the caster); every other spell needs one. Only handles a single target — for a spell that narratively hits several creatures, call this once per target.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "spell_name"],
				"properties": {
					"character_id": {"type": "string", "description": "The casting character's ID."},
					"spell_name": {"type": "string", "description": "The spell's exact name, e.g. \"Fireball\" — never guessed or abbreviated."},
					"target_character_id": {"type": "string", "description": "Optional — omit for a self-only spell."},
					"slot_level": {"type": "integer", "description": "Optional — cast using a higher-level slot than the spell's own minimum (upcasting). Omit to use the spell's own base level."}
				}
			}`),
		},
		{
			Name:        "melee_attack",
			Description: "Make a melee weapon attack — the only correct way to resolve a martial character's attack, the same real mechanical gate cast_spell already applies to spellcasting. The engine checks the attacker's currently-equipped weapon (never call this without one equipped) and rejects the attack if that weapon can't actually be used in melee (e.g. a bow) — never call apply_effect for a weapon attack instead, and never narrate a hit or miss until you've called this and seen the real result. A rejection is not a miss — it means the attack was never rolled at all (wrong weapon kind equipped, target out of reach, no action left this turn); a real roll that simply doesn't connect is still success=true with a miss narrated from result_message.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The attacking character's ID."},
					"target_character_id": {"type": "string", "description": "The target's ID — required; there is no self-attack."}
				}
			}`),
		},
		{
			Name:        "ranged_attack",
			Description: "Make a ranged weapon attack (a bow, crossbow, sling, or a thrown weapon used at range) — the same real mechanical gate melee_attack applies, for the ranged case. The engine checks the attacker's currently-equipped weapon and rejects the attack if it has neither Ammunition nor Thrown (e.g. a longsword can't be fired or thrown) — never call apply_effect for a weapon attack instead. A rejection means the attack was never rolled at all (wrong weapon kind equipped, target out of range/no line of sight); a real roll that simply misses is still success=true.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The attacking character's ID."},
					"target_character_id": {"type": "string", "description": "The target's ID — required; there is no self-attack."}
				}
			}`),
		},
		{
			Name:        "offhand_attack",
			Description: "Make a bonus-action attack with a character's equipped off-hand weapon (SRD Two-Weapon Fighting) — the real mechanical gate: the engine rejects this if either the main-hand or off-hand weapon lacks the Light property, or if no weapon is equipped in the off hand at all. Never adds the ability modifier to damage, per the core SRD rule. Call get_available_actions first if you're not sure whether this character has a legal off-hand attack available.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The attacking character's ID."},
					"target_character_id": {"type": "string", "description": "The target's ID — required; there is no self-attack."}
				}
			}`),
		},
		{
			Name:        "grapple",
			Description: "Attempt to grab and restrain a character within reach — a real opposed check (Strength (Athletics) against the target's better of Athletics/Acrobatics), not something you decide narratively. The engine rejects the attempt outright (never rolled) if the grappler has no hand free (both hands already hold weapons, or a two-handed weapon is equipped) — call get_available_actions first if you're unsure. A rejection is not a failed grapple; a real roll that simply loses the contest still succeeds=true, grappled=false.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The grappling character's ID."},
					"target_character_id": {"type": "string", "description": "The target's ID — required."}
				}
			}`),
		},
		{
			Name:        "shove",
			Description: "Attempt to knock a character prone or push it 5 feet away — the same real opposed check as grapple, no free hand required. Specify which effect you want; a rejection means the attempt was never rolled (out of reach, incapacitated), not a failed shove.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id", "effect"],
				"properties": {
					"character_id": {"type": "string", "description": "The shoving character's ID."},
					"target_character_id": {"type": "string", "description": "The target's ID — required."},
					"effect": {"type": "string", "enum": ["prone", "push"], "description": "\"prone\" knocks the target prone; \"push\" moves it 5 feet directly away."}
				}
			}`),
		},
		{
			Name:        "equip_item",
			Description: "Move an item already in a character's inventory into an equipment slot (ready a weapon, don armor, put on a ring). The item must already be carried — call receive_item first if it isn't. A shield goes in the \"shield\" slot even though the engine tracks it alongside off-hand weapons internally; use whichever of \"off_hand\"/\"shield\" matches what's actually being equipped.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "item_name", "slot"],
				"properties": {
					"character_id": {"type": "string", "description": "The character equipping the item."},
					"item_name": {"type": "string", "description": "Must already be a real member of this character's inventory."},
					"slot": {"type": "string", "enum": ["main_hand", "off_hand", "armor", "shield", "head", "neck", "shoulders", "hands", "waist", "feet", "ring_1", "ring_2"]}
				}
			}`),
		},
		{
			Name:        "unequip_item",
			Description: "Clear one of a character's equipment slots. The item stays in the character's inventory — this only changes what's readied, not what's carried.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "slot"],
				"properties": {
					"character_id": {"type": "string"},
					"slot": {"type": "string", "enum": ["main_hand", "off_hand", "armor", "shield", "head", "neck", "shoulders", "hands", "waist", "feet", "ring_1", "ring_2"]}
				}
			}`),
		},
		{
			Name:        "receive_item",
			Description: "Add a real item to a character's inventory (found treasure, a purchase, something handed to them narratively). item_name is resolved against the real item catalog — an unrecognized name is rejected, never invented. This adds exactly one item per call; for multiple identical items (e.g. 5 torches), call it once per item.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "item_name"],
				"properties": {
					"character_id": {"type": "string"},
					"item_name": {"type": "string", "description": "A real item name from the catalog — do not invent one."}
				}
			}`),
		},
		{
			Name:        "discard_item",
			Description: "Permanently remove a real item from a character's inventory (dropped, destroyed, thrown away). If the item was equipped, it's automatically unreadied first. There is no \"item on the ground\" to pick back up — this is permanent.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "item_name"],
				"properties": {
					"character_id": {"type": "string"},
					"item_name": {"type": "string", "description": "Must already be a real member of this character's inventory."}
				}
			}`),
		},
		{
			Name:        "give_item",
			Description: "Move a real item from one character's inventory directly into another's (a trade, a handoff, a gift). If the item is currently attuned, attunement ends. Giving an item away FROM a different player's character than the one whose narrative turn triggered this is subject to this campaign's PvP policy and may be rejected.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id", "item_name"],
				"properties": {
					"character_id": {"type": "string", "description": "The giving character's ID."},
					"target_character_id": {"type": "string", "description": "The receiving character's ID."},
					"item_name": {"type": "string", "description": "Must already be a real member of the giving character's inventory."}
				}
			}`),
		},
		{
			Name:        "generate_loot",
			Description: "Roll real CR-appropriate treasure for a roster of creatures — call this at encounter-prep time, right after create_npc-ing the monsters/NPCs you're about to throw at the party, NOT after the fight. Combining multiple creatures' challenge ratings into one group-appropriate roll is computed for real by the system engine. After it returns, place pieces of the result onto specific NPCs with add_currency/receive_item/equip_item — the wand goes on the wizard, not a random guard — so they can actually carry and use what they're holding during the fight. Every named character must have a real challenge_rating on its record (set it when you create_npc them) or the call is rejected.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_ids"],
				"properties": {
					"character_ids": {"type": "array", "items": {"type": "string"}, "description": "The encounter roster this roll is appropriate for — one or more character IDs, each with a real challenge_rating recorded."}
				}
			}`),
		},
		{
			Name:        "add_currency",
			Description: "Add copper/silver/gold/platinum to a character's inventory from nothing — place a generate_loot result onto a specific NPC at prep time, or directly reward a character narratively (e.g. \"you find a coin purse\"). No PvP gate — this only ever creates currency, never takes it from anyone.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id"],
				"properties": {
					"character_id": {"type": "string"},
					"copper": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"silver": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"gold": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"platinum": {"type": "integer", "description": "Defaults to 0 if omitted."}
				}
			}`),
		},
		{
			Name:        "transfer_currency",
			Description: "Move copper/silver/gold/platinum from one character's inventory into another's — looting a corpse's coin after combat, or a trade between characters. Fails if the source doesn't carry enough of a requested denomination (this does not make change across denominations). Taking currency away FROM a different player's character than the one whose narrative turn triggered this is subject to this campaign's PvP policy and may be rejected.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "target_character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The character the currency is coming from."},
					"target_character_id": {"type": "string", "description": "The receiving character's ID."},
					"copper": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"silver": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"gold": {"type": "integer", "description": "Defaults to 0 if omitted."},
					"platinum": {"type": "integer", "description": "Defaults to 0 if omitted."}
				}
			}`),
		},
		{
			Name:        "get_available_actions",
			Description: "Get the real, engine-computed list of everything a character can legally do right now — every equipped-weapon attack option (melee, ranged, and an off-hand/secondary weapon), Grapple and Shove options, and every currently-castable prepared/known spell, each against every other character currently in this campaign's active combat. Call this before improvising what a character can do in combat, or before choosing melee_attack/ranged_attack/cast_spell when you're not certain what's actually legal — it tells you exactly which option is real, so you never have to guess or narrate around a mechanical limitation (a melee-only weapon fired at range, a spell with no slots left, no free hand to grapple). If the character cannot act at all this turn (e.g. Paralyzed), can_act will be false with a real reason — narrate that, don't invent an action for them.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The character whose available actions to compute."}
				}
			}`),
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
		{
			Name:        "check_item_price",
			Description: "Look up an item's real price — this campaign's price_multiplier applied on top of the item's actual base value in the real item catalog. Never guess or invent a price; call this before narrating what something costs, and before vendor_sell_item/vendor_buy_item.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["item_name"],
				"properties": {
					"item_name": {"type": "string", "description": "A real item name from the catalog — do not invent one."}
				}
			}`),
		},
		{
			Name:        "list_vendor_inventory",
			Description: "Get the full, real list of items a vendor (or any character) actually has in inventory right now, each with its real price under this campaign's price_multiplier — the actual stock, not an abstract \"yes they have goods for sale\" summary. Call this before narrating what a shop has available, so you're describing what's mechanically real, not inventing a menu.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id"],
				"properties": {
					"character_id": {"type": "string", "description": "The vendor (or any character) whose inventory to list."}
				}
			}`),
		},
		{
			Name:        "vendor_sell_item",
			Description: "A vendor sells a real item from its own inventory to a buyer — the buyer pays the item's real price (this campaign's price_multiplier applied to the catalog value), the vendor receives the payment. Fails cleanly, with nothing moved, if the vendor doesn't actually stock the item or the buyer can't afford it. Taking the item FROM a different player's character than the one whose narrative turn triggered this is subject to this campaign's PvP policy.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["vendor_character_id", "buyer_character_id", "item_name"],
				"properties": {
					"vendor_character_id": {"type": "string", "description": "The character selling the item — must already have it in inventory."},
					"buyer_character_id": {"type": "string", "description": "The character paying for and receiving the item."},
					"item_name": {"type": "string", "description": "Must already be a real member of the vendor's inventory."}
				}
			}`),
		},
		{
			Name:        "vendor_buy_item",
			Description: "A vendor buys a real item from a seller's inventory — the vendor pays the item's real price (this campaign's price_multiplier applied to the catalog value) from its own funds, the seller receives the payment. Fails cleanly, with nothing moved, if the seller doesn't actually have the item or the vendor can't afford it (a vendor's own cash reserves are a real, finite constraint). Taking the item FROM a different player's character than the one whose narrative turn triggered this is subject to this campaign's PvP policy.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["vendor_character_id", "seller_character_id", "item_name"],
				"properties": {
					"vendor_character_id": {"type": "string", "description": "The character buying the item and paying for it."},
					"seller_character_id": {"type": "string", "description": "The character selling the item — must already have it in inventory."},
					"item_name": {"type": "string", "description": "Must already be a real member of the seller's inventory."}
				}
			}`),
		},
	}
}

// imageGenTool is the generate_scene_image DM tool (design doc §6.3) —
// kept separate from dmTools() since it's offered whenever an
// imagegen.Provider is configured (s.imageGen != nil), independent of
// whether a System Engine is configured at all, unlike every tool
// dmTools() returns.
func imageGenTool() llm.Tool {
	return llm.Tool{
		Name:        "generate_scene_image",
		Description: "Generate an illustration of the current scene from a text description. Call this sparingly — for a genuinely new or visually striking location/moment, not every narration beat — since each call is slow and costly. Write a complete, self-contained visual description; you won't get a second chance to add context.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"required": ["prompt"],
			"properties": {
				"prompt": {"type": "string", "description": "A complete, self-contained visual description of the scene to illustrate."}
			}
		}`),
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
// actingSenderID is the sender_id of the player whose narrative.
// player_input triggered this slow pass — apply_effect's PvP gate
// (design doc §9.1) needs it to tell "the DM affecting a different
// player's character" apart from ordinary NPC/monster damage, since
// nothing else in a tool call's own arguments identifies who initiated
// the action.
func (s *Server) callDMTool(ctx context.Context, campaignID, actingSenderID string, call llm.ToolCall) (result string, success bool, reasonCode string) {
	switch call.Name {
	case "resolve_check":
		return s.dmResolveCheck(ctx, campaignID, call.Arguments)
	case "apply_effect":
		return s.dmApplyEffect(ctx, campaignID, actingSenderID, call.Arguments)
	case "cast_spell":
		return s.dmCastSpell(ctx, campaignID, actingSenderID, call.Arguments)
	case "melee_attack":
		return s.dmAttack(ctx, campaignID, actingSenderID, call.Arguments, systemenginepb.AttackKind_ATTACK_KIND_MELEE)
	case "ranged_attack":
		return s.dmAttack(ctx, campaignID, actingSenderID, call.Arguments, systemenginepb.AttackKind_ATTACK_KIND_RANGED)
	case "offhand_attack":
		return s.dmAttack(ctx, campaignID, actingSenderID, call.Arguments, systemenginepb.AttackKind_ATTACK_KIND_OFFHAND)
	case "grapple":
		return s.dmGrapple(ctx, campaignID, actingSenderID, call.Arguments)
	case "shove":
		return s.dmShove(ctx, campaignID, actingSenderID, call.Arguments)
	case "equip_item":
		return s.dmEquipItem(ctx, campaignID, call.Arguments)
	case "unequip_item":
		return s.dmUnequipItem(ctx, campaignID, call.Arguments)
	case "receive_item":
		return s.dmReceiveItem(ctx, campaignID, call.Arguments)
	case "discard_item":
		return s.dmDiscardItem(ctx, campaignID, call.Arguments)
	case "give_item":
		return s.dmGiveItem(ctx, campaignID, actingSenderID, call.Arguments)
	case "generate_loot":
		return s.dmGenerateLoot(ctx, campaignID, call.Arguments)
	case "add_currency":
		return s.dmAddCurrency(ctx, campaignID, call.Arguments)
	case "transfer_currency":
		return s.dmTransferCurrency(ctx, campaignID, actingSenderID, call.Arguments)
	case "get_available_actions":
		return s.dmGetAvailableActions(ctx, campaignID, call.Arguments)
	case "get_character_status":
		return s.dmGetCharacterStatus(ctx, campaignID, call.Arguments)
	case "start_combat":
		return s.dmStartCombat(ctx, campaignID, call.Arguments)
	case "advance_turn":
		return s.dmAdvanceTurn(ctx, campaignID)
	case "end_combat":
		return s.dmEndCombat(ctx, campaignID)
	case "generate_combat_map":
		return s.dmGenerateCombatMap(ctx, campaignID)
	case "get_character_schema":
		return s.dmGetCharacterSchema(ctx)
	case "create_npc":
		return s.dmCreateNPC(ctx, campaignID, call.Arguments)
	case "generate_scene_image":
		return s.dmGenerateSceneImage(ctx, campaignID, call.Arguments)
	case "check_item_price":
		return s.dmCheckItemPrice(ctx, campaignID, call.Arguments)
	case "list_vendor_inventory":
		return s.dmListVendorInventory(ctx, campaignID, call.Arguments)
	case "vendor_sell_item":
		return s.dmVendorSellItem(ctx, campaignID, actingSenderID, call.Arguments)
	case "vendor_buy_item":
		return s.dmVendorBuyItem(ctx, campaignID, actingSenderID, call.Arguments)
	case "list_locations":
		return s.dmListLocations(ctx, campaignID)
	case "list_npcs":
		return s.dmListNPCs(ctx, campaignID)
	case "list_encounters":
		return s.dmListEncounters(ctx, campaignID)
	case "travel_to":
		return s.dmTravelTo(ctx, campaignID, call.Arguments)
	case "stash_item":
		return s.dmStashItem(ctx, campaignID, call.Arguments)
	case "retrieve_item":
		return s.dmRetrieveItem(ctx, campaignID, call.Arguments)
	case "stash_currency":
		return s.dmStashCurrency(ctx, campaignID, call.Arguments)
	case "retrieve_currency":
		return s.dmRetrieveCurrency(ctx, campaignID, call.Arguments)
	case "claim_location":
		return s.dmClaimLocation(ctx, campaignID, call.Arguments)
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

func (s *Server) dmApplyEffect(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
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

	// PvP gate (design doc §9.1): damage against a DIFFERENT player's
	// own character is a hostile PvP action, permitted only per the
	// campaign's configured policy — never left to the DM model's own
	// judgment (CLAUDE.md's "gates over prompting"). Healing another
	// player's character, or any effect against an NPC/monster
	// (OwnerID == masterSenderID, see dmCreateNPC) or the acting
	// player's own character, is unaffected.
	if args.EffectType == "damage" && character.OwnerID != "" && character.OwnerID != masterSenderID && character.OwnerID != actingSenderID {
		pol := s.campaignPolicy(ctx, campaignID)
		switch pol.PvPPolicy {
		case policy.PvPPolicyAllowed:
			// proceed
		case policy.PvPPolicyWithConsent:
			if !slices.Contains(pol.PvPConsent, character.OwnerID) {
				return fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP damage", character.OwnerID), false, "pvp_no_consent"
			}
		default: // PvPPolicyPveOnly, or an unrecognized/unspecified value — fail closed
			return fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to damage another player's character (%s)", character.OwnerID), false, "pvp_blocked"
		}
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

// dmCastSpell is the real mechanical gate against casting a spell that
// isn't prepared/known or that has no available slot (design doc §8,
// §9: "gates over prompting") — a thin wrapper around the System
// Engine's CastSpell RPC, which is itself a thin wrapper around
// OpenCombatEngine's own CastSpellAction. Before this existed, nothing
// stood between a player and an unprepared cast except the DM model's
// own narrative judgment (grounded in real character_data fed into
// every turn — see dm_slow_pass.go — but still just prompting).
//
// The PvP gate here is applied differently from dmApplyEffect's: Master
// cannot know in advance whether a *named spell* deals damage (unlike
// apply_effect, whose caller states effect_type=damage explicitly), so
// there is nothing to check before calling the engine. Instead, the
// engine reports whether the target actually took damage
// (CastSpellResponse.TargetDamaged — see that field's doc comment in
// protocol/system_engine.proto), and this function decides whether to
// persist that outcome: the caster's own mutation (slot consumed,
// concentration set) is always persisted — the spell was genuinely
// cast, the resource genuinely spent — but the target's mutation is
// discarded, not saved, when the campaign's PvP policy would have
// blocked it. Net effect: no disallowed damage ever reaches persisted
// game state, the same guarantee dmApplyEffect's up-front check gives,
// just enforced at the commit point instead of the call point since
// that's the earliest point Master actually has the information.
func (s *Server) dmCastSpell(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		SpellName         string `json:"spell_name"`
		TargetCharacterID string `json:"target_character_id"`
		SlotLevel         int32  `json:"slot_level"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.SpellName == "" {
		return "spell_name is required", false, "invalid_arguments"
	}

	caster, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	casterData := &structpb.Struct{}
	if err := protojson.Unmarshal(caster.CharacterData, casterData); err != nil {
		return fmt.Sprintf("parsing stored caster data: %v", err), false, "internal_error"
	}

	req := &systemenginepb.CastSpellRequest{
		RequestId:  "dm-tool-" + caster.ID,
		CampaignId: campaignID,
		Caster:     &systemenginepb.Actor{ActorId: caster.ID, CharacterData: casterData, SchemaVersion: caster.SchemaVersion},
		SpellName:  args.SpellName,
		SlotLevel:  args.SlotLevel,
	}

	var target store.Character
	hasTarget := args.TargetCharacterID != ""
	if hasTarget {
		target, err = s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
		if err != nil {
			return err.Error(), false, "character_not_found"
		}
		targetData := &structpb.Struct{}
		if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
			return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
		}
		req.Target = &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion}
		req.GridContext = s.buildGridContext(campaignID, caster.ID, target.ID)
	}

	resp, err := s.systemEngine.CastSpell(ctx, req)
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		// This is the hard gate itself — resp.Error is a real mechanical
		// rejection (not prepared, no slot, unknown spell name), computed
		// by the engine, not the model's own judgment. Same "repeat the
		// corrective instruction at the point of failure" reasoning as
		// dmStartCombat/dmAdvanceTurn — a general system-prompt rule alone
		// wasn't reliable enough for turn-order claims, so it isn't
		// trusted alone here either.
		return fmt.Sprintf("cast_spell FAILED: %s. Do not narrate this cast as if it succeeded — the spell was not cast.", resp.Error), false, "cast_spell_failed"
	}

	pvpBlocked := false
	pvpReason := ""
	pvpReasonCode := ""
	if hasTarget && resp.TargetDamaged && target.OwnerID != "" && target.OwnerID != masterSenderID && target.OwnerID != actingSenderID {
		pol := s.campaignPolicy(ctx, campaignID)
		switch pol.PvPPolicy {
		case policy.PvPPolicyAllowed:
			// proceed
		case policy.PvPPolicyWithConsent:
			if !slices.Contains(pol.PvPConsent, target.OwnerID) {
				pvpBlocked = true
				pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP damage", target.OwnerID)
				pvpReasonCode = "pvp_no_consent"
			}
		default: // PvPPolicyPveOnly, or an unrecognized/unspecified value — fail closed
			pvpBlocked = true
			pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to damage another player's character (%s)", target.OwnerID)
			pvpReasonCode = "pvp_blocked"
		}
	}

	// The caster's own mutation (slot consumed, concentration set) is
	// real regardless of the PvP gate above — the spell was genuinely
	// cast, the resource genuinely spent, even if it then fizzles
	// against a forbidden target.
	newCasterData, err := protojson.Marshal(resp.Caster.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated caster data: %v", err), false, "internal_error"
	}
	caster.CharacterData = newCasterData
	caster.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, caster); err != nil {
		return fmt.Sprintf("saving updated caster: %v", err), false, "internal_error"
	}

	if pvpBlocked {
		return pvpReason, false, pvpReasonCode
	}

	if hasTarget && resp.Target != nil {
		newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
		if err != nil {
			return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
		}
		target.CharacterData = newTargetData
		target.UpdatedAt = time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, target); err != nil {
			return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
		}
	}

	payload, err := json.Marshal(map[string]any{
		"cast":    true,
		"message": resp.ResultMessage,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmAttack is the real mechanical gate against a martial character's
// attack that previously had no RPC at all — melee_attack/ranged_attack
// both dispatch here with a different AttackKind (design doc §8, §9:
// "gates over prompting"), the same shape dmCastSpell already
// established for spellcasting. Modeled line-for-line on dmCastSpell:
// the attacker's own mutation (action economy spent) always persists —
// the attack genuinely happened, the resource genuinely spent — while
// the target's mutation is discarded, not saved, when the campaign's
// PvP policy would have blocked it (same post-hoc gate reasoning as
// dmCastSpell's own doc comment, since Master cannot know whether an
// attack will connect before calling the engine).
func (s *Server) dmAttack(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage, kind systemenginepb.AttackKind) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		TargetCharacterID string `json:"target_character_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.TargetCharacterID == "" {
		return "target_character_id is required — there is no self-attack", false, "invalid_arguments"
	}

	attacker, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	attackerData := &structpb.Struct{}
	if err := protojson.Unmarshal(attacker.CharacterData, attackerData); err != nil {
		return fmt.Sprintf("parsing stored attacker data: %v", err), false, "internal_error"
	}

	target, err := s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	targetData := &structpb.Struct{}
	if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
		return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
	}

	req := &systemenginepb.AttackRequest{
		RequestId:   "dm-tool-" + attacker.ID,
		CampaignId:  campaignID,
		Attacker:    &systemenginepb.Actor{ActorId: attacker.ID, CharacterData: attackerData, SchemaVersion: attacker.SchemaVersion},
		Target:      &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion},
		Kind:        kind,
		GridContext: s.buildGridContext(campaignID, attacker.ID, target.ID),
	}

	resp, err := s.systemEngine.Attack(ctx, req)
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		// The hard gate itself — resp.Error is a real mechanical rejection
		// (wrong weapon kind equipped, no weapon equipped, out of range/no
		// line of sight), computed by the engine, not the model's own
		// judgment. This is not a miss: the attack was never rolled.
		return fmt.Sprintf("attack FAILED: %s. Do not narrate this as a hit or a miss — the attack was never rolled.", resp.Error), false, "attack_failed"
	}

	pvpBlocked := false
	pvpReason := ""
	pvpReasonCode := ""
	if resp.TargetDamaged && target.OwnerID != "" && target.OwnerID != masterSenderID && target.OwnerID != actingSenderID {
		pol := s.campaignPolicy(ctx, campaignID)
		switch pol.PvPPolicy {
		case policy.PvPPolicyAllowed:
			// proceed
		case policy.PvPPolicyWithConsent:
			if !slices.Contains(pol.PvPConsent, target.OwnerID) {
				pvpBlocked = true
				pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP damage", target.OwnerID)
				pvpReasonCode = "pvp_no_consent"
			}
		default: // PvPPolicyPveOnly, or an unrecognized/unspecified value — fail closed
			pvpBlocked = true
			pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to damage another player's character (%s)", target.OwnerID)
			pvpReasonCode = "pvp_blocked"
		}
	}

	// The attacker's own mutation (action economy spent) is real
	// regardless of the PvP gate above — the attack genuinely happened,
	// even if it then can't land against a forbidden target.
	newAttackerData, err := protojson.Marshal(resp.Attacker.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated attacker data: %v", err), false, "internal_error"
	}
	attacker.CharacterData = newAttackerData
	attacker.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, attacker); err != nil {
		return fmt.Sprintf("saving updated attacker: %v", err), false, "internal_error"
	}

	if pvpBlocked {
		return pvpReason, false, pvpReasonCode
	}

	if resp.Target != nil {
		newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
		if err != nil {
			return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
		}
		target.CharacterData = newTargetData
		target.UpdatedAt = time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, target); err != nil {
			return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
		}
	}

	payload, err := json.Marshal(map[string]any{
		"attacked": true,
		"hit":      resp.Hit,
		"message":  resp.ResultMessage,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmGrapple is the real mechanical gate against a grapple attempt —
// modeled line-for-line on dmAttack, but gates on resp.Grappled instead
// of resp.TargetDamaged (Grapple never deals damage; it applies the
// Grappled condition, which is the PvP-relevant mutation here). The
// actor's own mutation (action economy spent) always persists — the
// attempt genuinely happened, the resource genuinely spent — while the
// target's mutation is discarded, not saved, when the campaign's PvP
// policy would have blocked it.
func (s *Server) dmGrapple(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		TargetCharacterID string `json:"target_character_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.TargetCharacterID == "" {
		return "target_character_id is required", false, "invalid_arguments"
	}

	actor, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	actorData := &structpb.Struct{}
	if err := protojson.Unmarshal(actor.CharacterData, actorData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	target, err := s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	targetData := &structpb.Struct{}
	if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
		return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
	}

	req := &systemenginepb.GrappleRequest{
		RequestId:   "dm-tool-" + actor.ID,
		CampaignId:  campaignID,
		Actor:       &systemenginepb.Actor{ActorId: actor.ID, CharacterData: actorData, SchemaVersion: actor.SchemaVersion},
		Target:      &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion},
		GridContext: s.buildGridContext(campaignID, actor.ID, target.ID),
	}

	resp, err := s.systemEngine.Grapple(ctx, req)
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return fmt.Sprintf("grapple FAILED: %s. Do not narrate this as a successful or failed grapple — the attempt was never rolled.", resp.Error), false, "grapple_failed"
	}

	pvpBlocked := false
	pvpReason := ""
	pvpReasonCode := ""
	if resp.Grappled && target.OwnerID != "" && target.OwnerID != masterSenderID && target.OwnerID != actingSenderID {
		pol := s.campaignPolicy(ctx, campaignID)
		switch pol.PvPPolicy {
		case policy.PvPPolicyAllowed:
			// proceed
		case policy.PvPPolicyWithConsent:
			if !slices.Contains(pol.PvPConsent, target.OwnerID) {
				pvpBlocked = true
				pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP effects", target.OwnerID)
				pvpReasonCode = "pvp_no_consent"
			}
		default:
			pvpBlocked = true
			pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to affect another player's character (%s)", target.OwnerID)
			pvpReasonCode = "pvp_blocked"
		}
	}

	newActorData, err := protojson.Marshal(resp.Actor.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated character data: %v", err), false, "internal_error"
	}
	actor.CharacterData = newActorData
	actor.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, actor); err != nil {
		return fmt.Sprintf("saving updated character: %v", err), false, "internal_error"
	}

	if pvpBlocked {
		return pvpReason, false, pvpReasonCode
	}

	if resp.Target != nil {
		newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
		if err != nil {
			return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
		}
		target.CharacterData = newTargetData
		target.UpdatedAt = time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, target); err != nil {
			return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
		}
	}

	payload, err := json.Marshal(map[string]any{
		"attempted": true,
		"grappled":  resp.Grappled,
		"message":   resp.ResultMessage,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmShove mirrors dmGrapple exactly, for a shove attempt — effect must
// be "prone" or "push" (dmShoveEffect maps it to the real proto enum;
// an unrecognized value is a real rejection, never guessed).
func (s *Server) dmShove(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		TargetCharacterID string `json:"target_character_id"`
		Effect            string `json:"effect"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.TargetCharacterID == "" {
		return "target_character_id is required", false, "invalid_arguments"
	}
	var effect systemenginepb.ShoveEffect
	switch args.Effect {
	case "prone":
		effect = systemenginepb.ShoveEffect_SHOVE_EFFECT_PRONE
	case "push":
		effect = systemenginepb.ShoveEffect_SHOVE_EFFECT_PUSH
	default:
		return fmt.Sprintf("invalid effect %q — must be \"prone\" or \"push\"", args.Effect), false, "invalid_arguments"
	}

	actor, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	actorData := &structpb.Struct{}
	if err := protojson.Unmarshal(actor.CharacterData, actorData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	target, err := s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	targetData := &structpb.Struct{}
	if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
		return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
	}

	req := &systemenginepb.ShoveRequest{
		RequestId:   "dm-tool-" + actor.ID,
		CampaignId:  campaignID,
		Actor:       &systemenginepb.Actor{ActorId: actor.ID, CharacterData: actorData, SchemaVersion: actor.SchemaVersion},
		Target:      &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion},
		Effect:      effect,
		GridContext: s.buildGridContext(campaignID, actor.ID, target.ID),
	}

	resp, err := s.systemEngine.Shove(ctx, req)
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return fmt.Sprintf("shove FAILED: %s. Do not narrate this as a successful or failed shove — the attempt was never rolled.", resp.Error), false, "shove_failed"
	}

	pvpBlocked := false
	pvpReason := ""
	pvpReasonCode := ""
	if resp.Shoved && target.OwnerID != "" && target.OwnerID != masterSenderID && target.OwnerID != actingSenderID {
		pol := s.campaignPolicy(ctx, campaignID)
		switch pol.PvPPolicy {
		case policy.PvPPolicyAllowed:
			// proceed
		case policy.PvPPolicyWithConsent:
			if !slices.Contains(pol.PvPConsent, target.OwnerID) {
				pvpBlocked = true
				pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP effects", target.OwnerID)
				pvpReasonCode = "pvp_no_consent"
			}
		default:
			pvpBlocked = true
			pvpReason = fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to affect another player's character (%s)", target.OwnerID)
			pvpReasonCode = "pvp_blocked"
		}
	}

	newActorData, err := protojson.Marshal(resp.Actor.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated character data: %v", err), false, "internal_error"
	}
	actor.CharacterData = newActorData
	actor.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, actor); err != nil {
		return fmt.Sprintf("saving updated character: %v", err), false, "internal_error"
	}

	if pvpBlocked {
		return pvpReason, false, pvpReasonCode
	}

	if resp.Target != nil {
		newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
		if err != nil {
			return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
		}
		target.CharacterData = newTargetData
		target.UpdatedAt = time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, target); err != nil {
			return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
		}
	}

	payload, err := json.Marshal(map[string]any{
		"attempted": true,
		"shoved":    resp.Shoved,
		"message":   resp.ResultMessage,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// parseEquipmentSlot maps a DM tool's slot string to the real proto
// enum — an unrecognized value is a real rejection, never guessed at,
// same reasoning as dmShove's effect string.
func parseEquipmentSlot(s string) (systemenginepb.EquipmentSlot, bool) {
	switch s {
	case "main_hand":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_MAIN_HAND, true
	case "off_hand":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_OFF_HAND, true
	case "armor":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_ARMOR, true
	case "shield":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_SHIELD, true
	case "head":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_HEAD, true
	case "neck":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_NECK, true
	case "shoulders":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_SHOULDERS, true
	case "hands":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_HANDS, true
	case "waist":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_WAIST, true
	case "feet":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_FEET, true
	case "ring_1":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_RING_1, true
	case "ring_2":
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_RING_2, true
	default:
		return systemenginepb.EquipmentSlot_EQUIPMENT_SLOT_UNSPECIFIED, false
	}
}

// dmEquipItem moves an item already in a character's inventory into an
// equipment slot. No PvP gate — same "DM has GM-level latitude over any
// character at the table" precedent every other DM tool relies on
// (campaignCharacter's own doc comment).
func (s *Server) dmEquipItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		ItemName    string `json:"item_name"`
		Slot        string `json:"slot"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	slot, ok := parseEquipmentSlot(args.Slot)
	if !ok {
		return fmt.Sprintf("invalid slot %q", args.Slot), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	resp, err := s.systemEngine.EquipItem(ctx, &systemenginepb.EquipItemRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		ItemName:   args.ItemName,
		Slot:       slot,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "equip_failed"
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

	payload, err := json.Marshal(map[string]any{"equipped": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmUnequipItem clears one of a character's equipment slots — the item
// stays in inventory. No PvP gate, same reasoning as dmEquipItem.
func (s *Server) dmUnequipItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		Slot        string `json:"slot"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	slot, ok := parseEquipmentSlot(args.Slot)
	if !ok {
		return fmt.Sprintf("invalid slot %q", args.Slot), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	resp, err := s.systemEngine.UnequipItem(ctx, &systemenginepb.UnequipItemRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Slot:       slot,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "unequip_failed"
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

	payload, err := json.Marshal(map[string]any{"unequipped": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmReceiveItem resolves item_name against the sidecar's real
// Open5e-backed item catalog and adds it to a character's inventory — an
// unrecognized name is a real rejection, never invented (CLAUDE.md's
// "gates over prompting"). No PvP gate: receiving an item is never
// hostile.
func (s *Server) dmReceiveItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		ItemName    string `json:"item_name"`
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

	resp, err := s.systemEngine.AddItemToInventory(ctx, &systemenginepb.AddItemToInventoryRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "receive_failed"
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

	payload, err := json.Marshal(map[string]any{"received": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmDiscardItem permanently removes a real item from a character's
// inventory — no PvP gate, since a character can only discard its own
// belongings (there is no target argument at all).
func (s *Server) dmDiscardItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		ItemName    string `json:"item_name"`
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

	resp, err := s.systemEngine.RemoveItemFromInventory(ctx, &systemenginepb.RemoveItemFromInventoryRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "discard_failed"
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

	payload, err := json.Marshal(map[string]any{"discarded": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmGiveItem moves a real item from one character's inventory into
// another's. Unlike dmGrapple/dmShove, the PvP gate here runs BEFORE
// calling the engine, not after: a transfer has no "the attempt still
// happened, only the outcome is blocked" half-state worth preserving —
// letting the item vanish from source without landing in target would
// be strictly worse than never having called the engine at all.
func (s *Server) dmGiveItem(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		TargetCharacterID string `json:"target_character_id"`
		ItemName          string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.TargetCharacterID == "" {
		return "target_character_id is required", false, "invalid_arguments"
	}

	source, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	sourceData := &structpb.Struct{}
	if err := protojson.Unmarshal(source.CharacterData, sourceData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	target, err := s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	targetData := &structpb.Struct{}
	if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
		return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
	}

	// PvP gate (design doc §9.1): taking an item away from a different
	// player's character without their own action driving it is the
	// same kind of hostile-without-consent effect apply_effect's damage
	// path already gates — checked BEFORE calling the engine, since
	// unlike grapple/shove a transfer has no "the attempt still
	// happened, only the outcome is blocked" half-state to preserve.
	// Exempt when source is dead: the party dividing up a fallen ally's
	// own things (who's carrying the body and gear from here on) is
	// logistics, not theft — the AI DM never asks whether the party
	// intends to raise them, that conversation stays entirely the
	// players' own. characterIsDead is a real engine-computed status
	// (turn_order.go), not the DM model's own say-so, so this stays a
	// real gate, not a prompting workaround. Only checked inside this
	// same-different-owner branch, same as before this exemption existed
	// — a self-owned or NPC-owned source never needed the extra engine
	// round trip and still doesn't.
	if reason, code, err := s.pvpGateBlocked(ctx, campaignID, actingSenderID, "an item", source); err != nil {
		return fmt.Sprintf("checking character status: %v", err), false, "engine_error"
	} else if reason != "" {
		return reason, false, code
	}

	resp, err := s.systemEngine.TransferItem(ctx, &systemenginepb.TransferItemRequest{
		RequestId:  "dm-tool-" + source.ID,
		CampaignId: campaignID,
		Source:     &systemenginepb.Actor{ActorId: source.ID, CharacterData: sourceData, SchemaVersion: source.SchemaVersion},
		Target:     &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "give_failed"
	}

	newSourceData, err := protojson.Marshal(resp.Source.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated character data: %v", err), false, "internal_error"
	}
	source.CharacterData = newSourceData
	source.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, source); err != nil {
		return fmt.Sprintf("saving updated character: %v", err), false, "internal_error"
	}

	newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
	}
	target.CharacterData = newTargetData
	target.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, target); err != nil {
		return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"given": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmGenerateLoot rolls CR-appropriate treasure for a roster of
// characters (design doc §8, §9: combining multiple creatures' CRs into
// one group-appropriate roll is real system-engine math, never computed
// here — see CLAUDE.md's system-engine boundary rule). Read-only: no
// character is mutated or persisted by this call, since it only
// produces a bundle for the DM to place via add_currency/receive_item/
// equip_item afterward.
func (s *Server) dmGenerateLoot(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterIDs []string `json:"character_ids"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if len(args.CharacterIDs) == 0 {
		return "character_ids is required and must not be empty", false, "invalid_arguments"
	}

	participants := make([]*systemenginepb.Actor, 0, len(args.CharacterIDs))
	for _, characterID := range args.CharacterIDs {
		character, err := s.campaignCharacter(ctx, campaignID, characterID)
		if err != nil {
			return err.Error(), false, "character_not_found"
		}
		characterData := &structpb.Struct{}
		if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
			return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
		}
		participants = append(participants, &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion})
	}

	resp, err := s.systemEngine.GenerateLoot(ctx, &systemenginepb.GenerateLootRequest{
		RequestId:    "dm-tool-" + participants[0].ActorId,
		CampaignId:   campaignID,
		Participants: participants,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "generate_loot_failed"
	}

	payload, err := json.Marshal(map[string]any{
		"copper":   resp.Copper,
		"silver":   resp.Silver,
		"gold":     resp.Gold,
		"platinum": resp.Platinum,
		"item":     resp.ItemName,
		"message":  resp.ResultMessage,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmAddCurrency places currency onto a character's inventory from
// nothing (a generate_loot placement, or a direct narrative reward). No
// PvP gate — same reasoning dmReceiveItem already has none.
func (s *Server) dmAddCurrency(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		Copper      int32  `json:"copper"`
		Silver      int32  `json:"silver"`
		Gold        int32  `json:"gold"`
		Platinum    int32  `json:"platinum"`
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

	resp, err := s.systemEngine.AddCurrency(ctx, &systemenginepb.AddCurrencyRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Copper:     args.Copper,
		Silver:     args.Silver,
		Gold:       args.Gold,
		Platinum:   args.Platinum,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "add_currency_failed"
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

	payload, err := json.Marshal(map[string]any{"added": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmTransferCurrency moves currency from one character's inventory into
// another's — mirrors dmGiveItem's exact shape, including running the
// PvP gate before calling the engine (a currency transfer has the same
// "no meaningful half-state" reasoning an item transfer does).
func (s *Server) dmTransferCurrency(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID       string `json:"character_id"`
		TargetCharacterID string `json:"target_character_id"`
		Copper            int32  `json:"copper"`
		Silver            int32  `json:"silver"`
		Gold              int32  `json:"gold"`
		Platinum          int32  `json:"platinum"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if args.TargetCharacterID == "" {
		return "target_character_id is required", false, "invalid_arguments"
	}

	source, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	sourceData := &structpb.Struct{}
	if err := protojson.Unmarshal(source.CharacterData, sourceData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	target, err := s.campaignCharacter(ctx, campaignID, args.TargetCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	targetData := &structpb.Struct{}
	if err := protojson.Unmarshal(target.CharacterData, targetData); err != nil {
		return fmt.Sprintf("parsing stored target data: %v", err), false, "internal_error"
	}

	// PvP gate (design doc §9.1) — see dmGiveItem's own doc comment for
	// why this runs before calling the engine, not post-hoc, and for why
	// a dead source (the party dividing up a fallen ally's own coin) is
	// exempt. Only checked inside this same-different-owner branch, same
	// as before this exemption existed — a self-owned or NPC-owned
	// source never needed the extra engine round trip and still doesn't.
	if reason, code, err := s.pvpGateBlocked(ctx, campaignID, actingSenderID, "currency", source); err != nil {
		return fmt.Sprintf("checking character status: %v", err), false, "engine_error"
	} else if reason != "" {
		return reason, false, code
	}

	resp, err := s.systemEngine.TransferCurrency(ctx, &systemenginepb.TransferCurrencyRequest{
		RequestId:  "dm-tool-" + source.ID,
		CampaignId: campaignID,
		Source:     &systemenginepb.Actor{ActorId: source.ID, CharacterData: sourceData, SchemaVersion: source.SchemaVersion},
		Target:     &systemenginepb.Actor{ActorId: target.ID, CharacterData: targetData, SchemaVersion: target.SchemaVersion},
		Copper:     args.Copper,
		Silver:     args.Silver,
		Gold:       args.Gold,
		Platinum:   args.Platinum,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "transfer_currency_failed"
	}

	newSourceData, err := protojson.Marshal(resp.Source.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated character data: %v", err), false, "internal_error"
	}
	source.CharacterData = newSourceData
	source.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, source); err != nil {
		return fmt.Sprintf("saving updated character: %v", err), false, "internal_error"
	}

	newTargetData, err := protojson.Marshal(resp.Target.CharacterData)
	if err != nil {
		return fmt.Sprintf("marshaling updated target data: %v", err), false, "internal_error"
	}
	target.CharacterData = newTargetData
	target.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, target); err != nil {
		return fmt.Sprintf("saving updated target: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"transferred": true, "message": resp.ResultMessage})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmGetAvailableActions computes the real, engine-derived list of
// everything characterID can legally do right now (design doc §8, §9:
// "gates over prompting") — candidate_targets is every OTHER character
// currently in this campaign's active combat (combatParticipantIDs,
// turn_order.go), the same "who's in the fight" source of truth
// advanceToNextActionableCharacter already uses; outside structured
// combat this is simply empty, and the response still reports whatever
// targetless options (self-only/AOE spells) apply. A candidate that
// fails to resolve is skipped rather than failing the whole call — a
// stale/removed participant shouldn't hide every other real option.
func (s *Server) dmGetAvailableActions(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
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

	req := &systemenginepb.GetAvailableActionsRequest{
		RequestId:  "dm-tool-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	}
	for _, participantID := range s.combatParticipantIDs(campaignID) {
		if participantID == character.ID {
			continue
		}
		participant, err := s.campaignCharacter(ctx, campaignID, participantID)
		if err != nil {
			continue
		}
		participantData := &structpb.Struct{}
		if err := protojson.Unmarshal(participant.CharacterData, participantData); err != nil {
			continue
		}
		req.CandidateTargets = append(req.CandidateTargets, &systemenginepb.Actor{ActorId: participant.ID, CharacterData: participantData, SchemaVersion: participant.SchemaVersion})
	}

	resp, err := s.systemEngine.GetAvailableActions(ctx, req)
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return fmt.Sprintf("get_available_actions FAILED: %s", resp.Error), false, "engine_error"
	}

	actions := make([]map[string]any, 0, len(resp.Actions))
	for _, a := range resp.Actions {
		actions = append(actions, map[string]any{
			"kind":                    a.Kind.String(),
			"label":                   a.Label,
			"source_name":             a.SourceName,
			"target_character_id":     a.TargetCharacterId,
			"action_economy_category": a.ActionEconomyCategory.String(),
		})
	}

	payload, err := json.Marshal(map[string]any{
		"can_act":           resp.CanAct,
		"cannot_act_reason": resp.CannotActReason,
		"has_action":        resp.HasAction,
		"has_bonus_action":  resp.HasBonusAction,
		"has_reaction":      resp.HasReaction,
		"actions":           actions,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
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
		// The corrective instruction is repeated here, not just in the
		// system prompt, because it's the failure itself that needs to
		// land, not a general rule stated once far earlier in context —
		// live testing found the model narrating success anyway despite
		// the system prompt already saying not to (see dm_slow_pass.go's
		// looksLikeUnearnedTurnOrderClaim for the code-level backstop for
		// when this still doesn't land).
		return fmt.Sprintf("start_combat FAILED, no turn order exists: %v. Do not narrate initiative, turn order, or who goes first — narrate the danger/fight without any of that.", err), false, "start_combat_failed"
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
		// See dmStartCombat's identical reasoning for repeating the
		// corrective instruction at the point of failure.
		return fmt.Sprintf("advance_turn FAILED, turn order was not updated: %v. Do not narrate whose turn it is now — narrate the scene without claiming a turn order change.", err), false, "advance_turn_failed"
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

// dmGenerateSceneImage handles the generate_scene_image DM tool (design
// doc §6.3): calls the configured imagegen.Provider with the campaign's
// effective image maturity-tier constraint (policy.CampaignPolicy.
// EffectiveImageMaturityTierPrompt — never more permissive than the
// campaign's text tier by default, see that method's doc comment), then
// broadcasts the result as narrative.scene_image so the whole table sees
// it, the same transparency principle as tool.result logging every DM
// tool call.
func (s *Server) dmGenerateSceneImage(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "prompt is required", false, "invalid_arguments"
	}

	pol := s.campaignPolicy(ctx, campaignID)
	fullPrompt := args.Prompt
	tierPrompt := pol.EffectiveImageMaturityTierPrompt()

	imageURL, err := s.imageGen.GenerateSceneImage(ctx, args.Prompt, tierPrompt)
	if err != nil {
		return fmt.Sprintf("generating scene image: %v", err), false, "image_gen_failed"
	}
	if tierPrompt != "" {
		fullPrompt = args.Prompt + "\n\nContent guidance: " + tierPrompt
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeNarrativeSceneImage, protocol.NarrativeSceneImagePayload{
		ImageURL: imageURL,
		Prompt:   fullPrompt,
	})
	if err != nil {
		return fmt.Sprintf("building narrative.scene_image message: %v", err), false, "internal_error"
	}
	recordEvent(ctx, s, msg)
	if err := broadcastMessage(s, msg); err != nil {
		return fmt.Sprintf("broadcasting narrative.scene_image: %v", err), false, "internal_error"
	}

	// Deliberately doesn't include imageURL in the tool result content:
	// the model has no use for the raw URL (Master already broadcasts
	// narrative.scene_image separately), and including it invited the
	// model to echo a markdown image link into its own narration text —
	// a real artifact observed live. A bare confirmation removes the
	// temptation at the source rather than only asking the system prompt
	// not to do it.
	return `{"generated": true}`, true, ""
}
