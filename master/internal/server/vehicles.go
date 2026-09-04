// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
)

// vehicleTools returns the DM tools for tracking real mounts/carts/
// wagons/ships — off-site possessions' other named half, alongside the
// stash tools in location.go (design doc §6.4's "off-site possessions
// (mounts, stashes)"). A vehicle here is never a character/creature
// record, even for an animal mount: this package only tracks who has it
// and where it is, not mechanical stats. A mount that needs real combat
// stats (AC, HP, speed) is still created as an ordinary character via
// create_npc/FromJson, same as any other creature — these tools exist
// for the thing the character/creature schema has no concept of at all.
// Gated in dm_slow_pass.go the same way campaignPackTools() is (needs a
// bound campaign pack for location_id to mean anything).
func vehicleTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "list_vehicles",
			Description: "Get the real, full list of every vehicle (mount, cart, wagon, ship, etc.) that exists for this campaign — id, name, type, and whether it's currently traveling with the party or stabled/docked at a specific location. Call this before narrating what the party has access to, rather than inventing one.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "acquire_vehicle",
			Description: "Create a real new vehicle (bought, built, found, given) — starts traveling with the party. Requires the party to actually be somewhere (travel_to first).",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["name", "vehicle_type"],
				"properties": {
					"name": {"type": "string", "description": "e.g. \"Old Nag\", \"the merchant wagon\", \"The Salty Gull\"."},
					"vehicle_type": {"type": "string", "description": "e.g. \"mount\", \"cart\", \"wagon\", \"ship\"."}
				}
			}`),
		},
		{
			Name:        "stable_vehicle",
			Description: "Leave a vehicle that's currently traveling with the party stabled/docked/parked at the party's current location. Only works for a vehicle that's actually traveling with the party right now — one already stabled elsewhere must be retrieved (take_vehicle) from where it actually is first.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["vehicle_id"],
				"properties": {
					"vehicle_id": {"type": "string", "description": "A real vehicle id from list_vehicles."}
				}
			}`),
		},
		{
			Name:        "take_vehicle",
			Description: "Bring a stabled vehicle along with the party again. Only succeeds for a vehicle actually stabled at the party's current location — you have to be there.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"required": ["vehicle_id"],
				"properties": {
					"vehicle_id": {"type": "string", "description": "A real vehicle id from list_vehicles, currently stabled at the party's current location."}
				}
			}`),
		},
	}
}

func (s *Server) dmListVehicles(ctx context.Context, campaignID string) (string, bool, string) {
	if s.vehicles == nil {
		return "vehicle tracking is not configured on this Master", false, "vehicles_not_configured"
	}
	vehicles, err := s.vehicles.ListVehicles(ctx, campaignID)
	if err != nil {
		return fmt.Sprintf("listing vehicles: %v", err), false, "internal_error"
	}

	list := make([]map[string]any, 0, len(vehicles))
	for _, v := range vehicles {
		entry := map[string]any{
			"id":           v.ID,
			"name":         v.Name,
			"vehicle_type": v.VehicleType,
			"stabled":      v.Stabled,
		}
		if v.Stabled {
			entry["location"] = v.LocationID
		}
		list = append(list, entry)
	}

	payload, err := json.Marshal(map[string]any{"vehicles": list})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmAcquireVehicle(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	if s.vehicles == nil {
		return "vehicle tracking is not configured on this Master", false, "vehicles_not_configured"
	}
	var args struct {
		Name        string `json:"name"`
		VehicleType string `json:"vehicle_type"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	// Same "the party has to be somewhere" gate as stash_item/
	// stash_currency — a vehicle acquired before any travel_to has no
	// meaningful "traveling with the party from where" context.
	partyLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if partyLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	id, err := newRandomID()
	if err != nil {
		return fmt.Sprintf("generating vehicle id: %v", err), false, "internal_error"
	}
	if err := s.vehicles.CreateVehicle(ctx, id, campaignID, args.Name, args.VehicleType); err != nil {
		return fmt.Sprintf("creating vehicle: %v", err), false, "internal_error"
	}
	// Best-effort: the vehicle itself is already real and saved: a
	// broadcast failure shouldn't turn a successful creation into a
	// failed tool call.
	if err := s.broadcastVehicleImported(ctx, campaignID, id, args.Name, args.VehicleType); err != nil {
		s.logger.Warn("failed to broadcast vehicle.imported", "error", err, "campaign_id", campaignID, "vehicle_id", id)
	}

	payload, err := json.Marshal(map[string]any{"acquired": true, "id": id, "name": args.Name, "vehicle_type": args.VehicleType})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmStableVehicle(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	if s.vehicles == nil {
		return "vehicle tracking is not configured on this Master", false, "vehicles_not_configured"
	}
	var args struct {
		VehicleID string `json:"vehicle_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	partyLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if partyLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	vehicle, ok, err := s.vehicles.GetVehicle(ctx, campaignID, args.VehicleID)
	if err != nil {
		return fmt.Sprintf("looking up vehicle: %v", err), false, "internal_error"
	}
	if !ok {
		return fmt.Sprintf("no vehicle with id %q in this campaign", args.VehicleID), false, "vehicle_not_found"
	}
	if vehicle.Stabled {
		return fmt.Sprintf("%s is already stabled at %q — take_vehicle from there first", vehicle.Name, vehicle.LocationID), false, "already_stabled"
	}

	if _, err := s.vehicles.StableVehicle(ctx, campaignID, args.VehicleID, partyLocation); err != nil {
		return fmt.Sprintf("stabling vehicle: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"stabled": true, "id": args.VehicleID, "location": partyLocation})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

func (s *Server) dmTakeVehicle(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	if s.vehicles == nil {
		return "vehicle tracking is not configured on this Master", false, "vehicles_not_configured"
	}
	var args struct {
		VehicleID string `json:"vehicle_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	partyLocation, err := s.currentPartyLocation(ctx, campaignID)
	if err != nil {
		return err.Error(), false, "no_campaign_pack"
	}
	if partyLocation == "" {
		return "the party has no current location yet — travel_to somewhere first", false, "no_party_location"
	}

	vehicle, ok, err := s.vehicles.GetVehicle(ctx, campaignID, args.VehicleID)
	if err != nil {
		return fmt.Sprintf("looking up vehicle: %v", err), false, "internal_error"
	}
	if !ok {
		return fmt.Sprintf("no vehicle with id %q in this campaign", args.VehicleID), false, "vehicle_not_found"
	}
	if !vehicle.Stabled {
		return fmt.Sprintf("%s is already traveling with the party", vehicle.Name), false, "not_stabled"
	}
	if vehicle.LocationID != partyLocation {
		return fmt.Sprintf("%s is stabled at %q, not the party's current location (%q)", vehicle.Name, vehicle.LocationID, partyLocation), false, "wrong_location"
	}

	if _, err := s.vehicles.TakeVehicle(ctx, campaignID, args.VehicleID); err != nil {
		return fmt.Sprintf("taking vehicle: %v", err), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{"taken": true, "id": args.VehicleID})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// importVehicle handles a real vehicle.import message (design doc
// §6.4) — a player directly declaring a new vehicle the party now has,
// independent of the DM's own narrative tool loop. No mechanical
// validation the way character.upload has (a vehicle carries no system-
// engine schema at all — see this file's own top doc comment); Name/
// VehicleType are accepted as given beyond a real "not blank" check.
// Broadcasts vehicle.imported the same way dmAcquireVehicle does, so
// every client learns about a new shared vehicle identically regardless
// of which path created it.
func (s *Server) importVehicle(ctx context.Context, conn *websocket.Conn, campaignID string, req protocol.VehicleImportMessage) error {
	if s.vehicles == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("vehicle import unavailable: vehicle tracking is not configured"))
	}
	if req.Payload.Name == "" || req.Payload.VehicleType == "" {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("name and vehicle_type are required"))
	}

	id, err := newRandomID()
	if err != nil {
		return err
	}
	if err := s.vehicles.CreateVehicle(ctx, id, campaignID, req.Payload.Name, req.Payload.VehicleType); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("creating vehicle: %w", err))
	}

	return s.broadcastVehicleImported(ctx, campaignID, id, req.Payload.Name, req.Payload.VehicleType)
}

// broadcastVehicleImported builds, persists, and broadcasts a
// vehicle.imported message to the whole campaign — shared by
// importVehicle (a real vehicle.import) and dmAcquireVehicle (the DM's
// own acquire_vehicle tool), so both creation paths notify the table
// identically. Best-effort from dmAcquireVehicle's own perspective: a
// broadcast failure here doesn't retroactively fail a tool call whose
// underlying vehicle creation already succeeded — see that call site.
func (s *Server) broadcastVehicleImported(ctx context.Context, campaignID, vehicleID, name, vehicleType string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeVehicleImported, protocol.VehicleImportedPayload{
		VehicleID:   vehicleID,
		Name:        name,
		VehicleType: vehicleType,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	return broadcastMessage(s, msg)
}
