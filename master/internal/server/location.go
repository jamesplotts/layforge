// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/campaignpack"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// locationTools returns the DM tools built on a bound campaign pack
// (design doc §6.4) — location tracking, travel, and off-site
// possessions (stashed items/currency, land holdings). Offered
// alongside dmTools() under the same system-engine gate (see the call
// site in dm_slow_pass.go) — half of these tools call real engine RPCs
// (RemoveItemFromInventory/RemoveCurrency/AddItemToInventory/
// AddCurrency), so gating the category as a whole avoids a confusing
// mix of working and always-failing tools.
func locationTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "list_locations",
			Description: "Get the real, full list of every location in this campaign's bound pack — id, which other locations it connects to, whether the party has discovered it, and whether the party has claimed it as a land holding. Call this before narrating what's reachable from here, or before travel_to, so you're describing the real map, not inventing one.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "travel_to",
			Description: "Move the party to a real location — legal only if location_id is directly connected to the party's current location (list_locations shows the real connection graph), or if the party has no current location yet (the very first move of a session, which may go to any real location). Marks the destination discovered.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["location_id"],
				"properties": {
					"location_id": {"type": "string", "description": "A real location id from list_locations — do not invent one."}
				}
			}`),
		},
		{
			Name:        "stash_item",
			Description: "Leave a real item from a character's inventory at the party's current location (a cache, a stash, gear left behind before a dangerous push) — removed from the character's carried inventory for real, recorded at this exact location. Requires the party to actually be somewhere (travel_to first).",
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
			Name:        "retrieve_item",
			Description: "Pick a previously stashed item back up into a character's inventory. Only succeeds for an item actually stashed at the party's current location — you have to be there; stashing it somewhere else and retrieving it here doesn't work.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["character_id", "item_name"],
				"properties": {
					"character_id": {"type": "string"},
					"item_name": {"type": "string", "description": "Must have been stashed at the party's current location."}
				}
			}`),
		},
		{
			Name:        "stash_currency",
			Description: "Leave copper/silver/gold/platinum from a character's own currency at the party's current location — removed from the character for real. Requires the party to actually be somewhere (travel_to first).",
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
			Name:        "retrieve_currency",
			Description: "Withdraw previously stashed currency back into a character's inventory. Only succeeds up to what's actually stashed at the party's current location.",
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
			Name:        "claim_location",
			Description: "Mark the party's current location as a land holding — a real, persistent flag (with an optional note) recorded on this exact location, not narrative flavor that evaporates. Requires the party to actually be somewhere (travel_to first).",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"note": {"type": "string", "description": "Optional — what the claim is about (e.g. \"cleared and garrisoned\")."}
				}
			}`),
		},
	}
}

// loadBoundPack loads campaignID's bound campaign pack from disk — a
// real rejection (not a zero-value success) when no campaignPack store
// is configured, no pack has ever been bound, or the bound directory no
// longer parses (moved, edited badly since binding).
func (s *Server) loadBoundPack(ctx context.Context, campaignID string) (campaignpack.Pack, error) {
	if s.campaignPack == nil {
		return campaignpack.Pack{}, fmt.Errorf("campaign packs are not configured on this Master")
	}
	binding, ok, err := s.campaignPack.GetCampaignPack(ctx, campaignID)
	if err != nil {
		return campaignpack.Pack{}, fmt.Errorf("looking up bound campaign pack: %w", err)
	}
	if !ok {
		return campaignpack.Pack{}, fmt.Errorf("no campaign pack is bound to this campaign")
	}
	pack, err := campaignpack.LoadPack(binding.PackDir)
	if err != nil {
		return campaignpack.Pack{}, fmt.Errorf("loading bound campaign pack: %w", err)
	}
	return pack, nil
}

// findLocation returns the location in pack whose id matches locationID.
func findLocation(pack campaignpack.Pack, locationID string) (campaignpack.Location, bool) {
	for _, loc := range pack.Locations {
		if loc.ID == locationID {
			return loc, true
		}
	}
	return campaignpack.Location{}, false
}

// currentPartyLocation returns campaignID's current party location id,
// or an error when campaignPack isn't configured — distinct from an
// empty-string "not set yet" result, which is a real, valid state (the
// bootstrap case) callers must check for themselves.
func (s *Server) currentPartyLocation(ctx context.Context, campaignID string) (string, error) {
	if s.campaignPack == nil {
		return "", fmt.Errorf("campaign packs are not configured on this Master")
	}
	return s.campaignPack.GetPartyLocation(ctx, campaignID)
}

// locationContextText returns a "Current location: ..." block for
// runSlowPass's userContent (dm_slow_pass.go) — best-effort, empty when
// no pack is bound or nothing has loaded/parsed, the same
// don't-fail-the-turn-over-optional-context reasoning the character-data
// section there already uses. Grounds the model in the real current
// location and its real connections, instead of it having to call
// list_locations on every single turn just to know where the party is.
func (s *Server) locationContextText(ctx context.Context, campaignID string) string {
	pack, err := s.loadBoundPack(ctx, campaignID)
	if err != nil {
		return ""
	}
	partyLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil || partyLocation == "" {
		return ""
	}
	current, ok := findLocation(pack, partyLocation)
	if !ok {
		return ""
	}
	return fmt.Sprintf("Current location: %s (connects to: %s)\n", current.ID, strings.Join(current.Connections, ", "))
}

func (s *Server) dmListLocations(ctx context.Context, campaignID string) (string, bool, string) {
	pack, err := s.loadBoundPack(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	partyLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}

	locations := make([]map[string]any, 0, len(pack.Locations))
	for _, loc := range pack.Locations {
		state, _, err := s.campaignPack.GetLocationState(ctx, campaignID, loc.ID)
		if err != nil {
			return fmt.Sprintf("getting state for location %q: %v", loc.ID, err), false, "internal_error"
		}
		locations = append(locations, map[string]any{
			"id":          loc.ID,
			"connections": loc.Connections,
			"discovered":  state.Discovered,
			"claimed":     state.ClaimedByParty,
			"claim_note":  state.ClaimNote,
		})
	}

	payload, err := json.Marshal(map[string]any{"party_location": partyLocation, "locations": locations})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmTravelTo(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		LocationID string `json:"location_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	pack, err := s.loadBoundPack(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	destination, ok := findLocation(pack, args.LocationID)
	if !ok {
		return fmt.Sprintf("%q is not a real location in this campaign's bound pack", args.LocationID), false, "location_not_found"
	}

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation != "" {
		current, ok := findLocation(pack, currentLocation)
		if !ok || !slices.Contains(current.Connections, destination.ID) {
			return fmt.Sprintf("%q is not reachable from the party's current location (%q)", destination.ID, currentLocation), false, "not_reachable"
		}
	}

	if err := s.campaignPack.SetPartyLocation(ctx, campaignID, destination.ID); err != nil {
		return fmt.Sprintf("recording party location: %v", err), false, "internal_error"
	}
	if err := s.campaignPack.SetLocationDiscovered(ctx, campaignID, destination.ID); err != nil {
		return fmt.Sprintf("recording location discovered: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{
		"arrived_at":  destination.ID,
		"description": destination.Body,
		"connections": destination.Connections,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmStashItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		ItemName    string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
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
		RequestId:  "dm-tool-stash-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "stash_failed"
	}
	if err := s.saveUpdatedCharacter(ctx, character, resp.Actor); err != nil {
		return err.Error(), false, "internal_error"
	}

	id, err := newRandomID()
	if err != nil {
		return fmt.Sprintf("generating stash id: %v", err), false, "internal_error"
	}
	if err := s.campaignPack.StashItem(ctx, id, campaignID, currentLocation, args.CharacterID, args.ItemName); err != nil {
		return fmt.Sprintf("recording stashed item: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"stashed": true, "location": currentLocation, "item": args.ItemName})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmRetrieveItem(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
		ItemName    string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	found, err := s.campaignPack.RetrieveItem(ctx, campaignID, currentLocation, args.CharacterID, args.ItemName)
	if err != nil {
		return fmt.Sprintf("retrieving stashed item: %v", err), false, "internal_error"
	}
	if !found {
		return fmt.Sprintf("no %q stashed by %s at the party's current location", args.ItemName, args.CharacterID), false, "nothing_stashed_here"
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
		RequestId:  "dm-tool-retrieve-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "retrieve_failed"
	}
	if err := s.saveUpdatedCharacter(ctx, character, resp.Actor); err != nil {
		return err.Error(), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"retrieved": true, "location": currentLocation, "item": args.ItemName})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmStashCurrency(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
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

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	resp, err := s.systemEngine.RemoveCurrency(ctx, &systemenginepb.RemoveCurrencyRequest{
		RequestId:  "dm-tool-stash-currency-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Copper:     args.Copper, Silver: args.Silver, Gold: args.Gold, Platinum: args.Platinum,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "insufficient_funds"
	}
	if err := s.saveUpdatedCharacter(ctx, character, resp.Actor); err != nil {
		return err.Error(), false, "internal_error"
	}

	if err := s.campaignPack.AddStashedCurrency(ctx, campaignID, currentLocation, args.CharacterID, args.Copper, args.Silver, args.Gold, args.Platinum); err != nil {
		return fmt.Sprintf("recording stashed currency: %v", err), false, "internal_error"
	}

	copper, silver, gold, platinum, err := s.campaignPack.GetStashedCurrency(ctx, campaignID, currentLocation, args.CharacterID)
	if err != nil {
		return fmt.Sprintf("reading stashed currency total: %v", err), false, "internal_error"
	}
	payload, err := json.Marshal(map[string]any{
		"stashed": true, "location": currentLocation,
		"total_copper": copper, "total_silver": silver, "total_gold": gold, "total_platinum": platinum,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmRetrieveCurrency(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
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

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	if err := s.campaignPack.RemoveStashedCurrency(ctx, campaignID, currentLocation, args.CharacterID, args.Copper, args.Silver, args.Gold, args.Platinum); err != nil {
		if errors.Is(err, store.ErrInsufficientStashedCurrency) {
			return "not enough currency stashed at the party's current location", false, "insufficient_stashed_currency"
		}
		return fmt.Sprintf("withdrawing stashed currency: %v", err), false, "internal_error"
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
		RequestId:  "dm-tool-retrieve-currency-" + character.ID,
		CampaignId: campaignID,
		Actor:      &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
		Copper:     args.Copper, Silver: args.Silver, Gold: args.Gold, Platinum: args.Platinum,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !resp.Success {
		return resp.Error, false, "retrieve_failed"
	}
	if err := s.saveUpdatedCharacter(ctx, character, resp.Actor); err != nil {
		return err.Error(), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"retrieved": true, "location": currentLocation})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmClaimLocation(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	currentLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if currentLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	if err := s.campaignPack.SetLocationClaimed(ctx, campaignID, currentLocation, args.Note); err != nil {
		return fmt.Sprintf("recording land claim: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"claimed": true, "location": currentLocation})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}
