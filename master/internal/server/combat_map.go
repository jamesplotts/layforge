// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math/rand"
	"time"

	"github.com/coder/websocket"

	"github.com/jamesplotts/layforge/master/internal/combatmap"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// Generation parameters for generate_combat_map — fixed for this version
// rather than DM-tool arguments (design doc doesn't call for map-size
// customization yet, and a fixed size is enough for a single tactical
// encounter, the scope this feature targets — see internal/combatmap's
// package doc comment).
const (
	combatMapWidth        = 30
	combatMapHeight       = 20
	combatMapRoomAttempts = 18
	combatMapMinRoomSize  = 4
	combatMapMaxRoomSize  = 9
	// combatMapVisionRadius bounds fog-of-war distance (in cells) — SRD
	// darkvision/torch-radius rules aren't modeled; this is a flat "can
	// see reasonably far down a lit corridor" default, not a mechanical
	// ruling.
	combatMapVisionRadius = 15
	// combatMapDefaultSpeedFeet is used when a character's own
	// combatStats.speed can't be read (see characterCombatInfo's doc
	// comment on why this package reads it directly at all) — the SRD's
	// own common humanoid default, chosen so movement validation degrades
	// to "reasonable" rather than "nothing can move" for a character
	// whose data doesn't have this field.
	combatMapDefaultSpeedFeet = 30
)

// combatMapMeta is one campaign's active combat map, plus the
// presentation metadata (owner, display color) decided once at
// generation time and reused for the rest of that combat — computed
// once so a later move never risks recomputing a token's color/owner
// incorrectly relative to every other token, the way deriving it fresh
// per move would (a token's color must stay stable across the whole
// encounter, not be re-derived relative to whichever token just moved).
type combatMapMeta struct {
	state  *combatmap.State
	owners map[string]string     // characterID -> OwnerID
	colors map[string]color.RGBA // characterID -> display color
}

// characterCombatInfo is the small subset of a character's stored JSON
// this package reads directly, rather than treating character_data as
// fully opaque the way most of Master's code does (CLAUDE.md: "System
// engine calls go through the gRPC contract... don't take a shortcut that
// assumes D&D/OpenCombatEngine specifically"). This is a real, narrow
// exception, not an oversight: combat-map tracking is explicitly
// Master-owned, informational state that never reaches the System Engine
// (internal/combatmap's package doc comment) — there's no gRPC call to
// route this through even in principle. A system engine whose schema
// doesn't have combatStats.speed at this path degrades gracefully
// (combatMapDefaultSpeedFeet) rather than breaking outright; Name/Team
// are purely cosmetic (label/clustering) and equally tolerant of being
// absent.
type characterCombatInfo struct {
	Name        string `json:"name"`
	Team        string `json:"team"`
	CombatStats struct {
		Speed int `json:"speed"`
	} `json:"combatStats"`
}

func parseCombatInfo(characterData json.RawMessage) characterCombatInfo {
	var info characterCombatInfo
	_ = json.Unmarshal(characterData, &info) // best-effort; zero value is a safe fallback (see speedFeet below)
	return info
}

func (info characterCombatInfo) speedFeet() int {
	if info.CombatStats.Speed <= 0 {
		return combatMapDefaultSpeedFeet
	}
	return info.CombatStats.Speed
}

// tokenColor deterministically derives a display color from characterID,
// split into two hue families by team so a table can tell "us" from
// "them" at a glance without needing real token art (see
// combatmap.RenderToken's own doc comment on why there's no art/label
// support yet). Not cryptographic, doesn't need to be — just a stable,
// visually-distinct color per character.
func tokenColor(characterID string, sameTeamAsFirst bool) color.RGBA {
	var hash uint32
	for i := 0; i < len(characterID); i++ {
		hash = hash*31 + uint32(characterID[i])
	}
	if sameTeamAsFirst {
		return color.RGBA{60 + uint8(hash%120), 120 + uint8((hash>>8)%100), 220, 255} // blue family
	}
	return color.RGBA{220, 90 + uint8(hash%100), 60 + uint8((hash>>8)%100), 255} // red family
}

// dmGenerateCombatMap generates a grid map for the campaign's currently
// active combat (turn_order.go) and places every combatant's token on
// it, auto-clustered by team (see clusterPosition) — there is no
// drag-and-drop placement UI in this version, only generation followed by
// map.token_move_request. Deliberately a separate, opt-in DM tool rather
// than something start_combat always does: not every fight has (or needs)
// real tactical position tracking — the same "call it when it's actually
// relevant" reasoning dmGenerateSceneImage's own tool description already
// uses for scene illustrations. Requires active combat (start_combat
// already called) since the map's whole point is tracking that
// encounter's combatants; the roster comes from turnOrder's own order,
// not a separate argument, since re-declaring it would just be a second
// source of truth for the same roster.
func (s *Server) dmGenerateCombatMap(ctx context.Context, campaignID string) (string, bool, string) {
	s.turnOrdersMu.Lock()
	turn, active := s.turnOrders[campaignID]
	var roster []string
	if active && turn.active {
		roster = append(roster, turn.order...)
	}
	s.turnOrdersMu.Unlock()

	if len(roster) == 0 {
		return "generate_combat_map FAILED: no active combat for this campaign. Call start_combat first.", false, "no_active_combat"
	}

	grid := combatmap.GenerateRoomsAndCorridors(combatmap.GenerateOptions{
		Width: combatMapWidth, Height: combatMapHeight,
		RoomAttempts: combatMapRoomAttempts,
		MinRoomSize:  combatMapMinRoomSize, MaxRoomSize: combatMapMaxRoomSize,
		Rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	})
	if grid == nil {
		return "generate_combat_map FAILED: map generation returned nothing.", false, "internal_error"
	}
	openCells := scanOpenCells(grid)
	if len(openCells) == 0 {
		return "generate_combat_map FAILED: generated map has no open floor at all.", false, "internal_error"
	}

	meta := &combatMapMeta{
		state:  combatmap.NewState(grid),
		owners: make(map[string]string, len(roster)),
		colors: make(map[string]color.RGBA, len(roster)),
	}

	var firstTeam string
	for i, characterID := range roster {
		character, err := s.campaignCharacter(ctx, campaignID, characterID)
		if err != nil {
			return fmt.Sprintf("generate_combat_map FAILED: %v", err), false, "character_not_found"
		}
		meta.owners[characterID] = character.OwnerID
		info := parseCombatInfo(character.CharacterData)
		if i == 0 {
			firstTeam = info.Team
		}
		sameTeam := info.Team == firstTeam
		meta.colors[characterID] = tokenColor(characterID, sameTeam)

		x, y := clusterPosition(openCells, i, sameTeam)
		tokenID, err := newMessageID() // any collision-resistant ID generator works here; reusing newMessageID avoids a second one
		if err != nil {
			return fmt.Sprintf("generate_combat_map FAILED: %v", err), false, "internal_error"
		}
		meta.state.PlaceToken(combatmap.Token{TokenID: tokenID, CharacterID: characterID, X: x, Y: y})
	}

	s.combatMapsMu.Lock()
	s.combatMaps[campaignID] = meta
	s.combatMapsMu.Unlock()

	s.broadcastCombatMapToEveryOwner(campaignID, meta)

	payload, err := json.Marshal(map[string]any{
		"generated":   true,
		"width":       grid.Width,
		"height":      grid.Height,
		"token_count": len(meta.state.Tokens),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// scanOpenCells lists every open (non-wall) cell of g in row-major scan
// order — used to place token clusters without assuming any particular
// generated layout (a generated map's rooms could land anywhere).
func scanOpenCells(g *combatmap.Grid) []combatmap.Position {
	var open []combatmap.Position
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			if g.Open(x, y) {
				open = append(open, combatmap.Position{X: x, Y: y})
			}
		}
	}
	return open
}

// clusterPosition picks a placement for combatant index i, clustering the
// first-seen team near the start of scan order and every other team near
// the end — a simple, layout-agnostic stand-in for "the party starts on
// one side of the room, the monsters on the other" that works regardless
// of where the generator actually put its rooms.
func clusterPosition(openCells []combatmap.Position, i int, firstTeamCluster bool) (x, y int) {
	if firstTeamCluster {
		idx := i % len(openCells)
		return openCells[idx].X, openCells[idx].Y
	}
	idx := (len(openCells) - 1 - i) % len(openCells)
	if idx < 0 {
		idx += len(openCells)
	}
	return openCells[idx].X, openCells[idx].Y
}

// broadcastCombatMapToEveryOwner sends every distinct character owner in
// meta their own fog-of-war-filtered map.token_state — never Broadcast,
// since two players legitimately see different content for the same
// underlying map (see sendToSender's doc comment). A player owning more
// than one token on the map sees the union of what each of their own
// characters can see.
func (s *Server) broadcastCombatMapToEveryOwner(campaignID string, meta *combatMapMeta) {
	byOwner := make(map[string][]combatmap.Token)
	for _, tok := range meta.state.Tokens {
		owner := meta.owners[tok.CharacterID]
		byOwner[owner] = append(byOwner[owner], tok)
	}

	for owner, ownedTokens := range byOwner {
		if owner == "" || owner == masterSenderID {
			continue // an NPC/monster with no real player connection to send to
		}

		visible := map[combatmap.Position]bool{}
		for _, tok := range ownedTokens {
			for pos := range combatmap.Visible(meta.state.Grid, tok.X, tok.Y, combatMapVisionRadius) {
				visible[pos] = true
			}
		}

		if err := s.sendCombatMapState(campaignID, owner, meta, visible); err != nil {
			s.logger.Warn("failed to send map.token_state", "error", err, "campaign_id", campaignID, "owner", owner)
		}
	}
}

// sendCombatMapState builds and sends recipient's own fog-of-war-filtered
// map.token_state: the visible cell grid, the visible tokens, and a
// composited PNG of the same (combatmap.Render) as a convenience for the
// reference client's thumbnail — see MapTokenStatePayload's doc comment
// for why both a structured grid and a rendered image are included.
func (s *Server) sendCombatMapState(campaignID, recipient string, meta *combatMapMeta, visible map[combatmap.Position]bool) error {
	grid := meta.state.Grid
	var cells []protocol.GridCellPayload
	for y := 0; y < grid.Height; y++ {
		for x := 0; x < grid.Width; x++ {
			if !visible[combatmap.Position{X: x, Y: y}] {
				continue
			}
			cell, _ := grid.At(x, y)
			cells = append(cells, protocol.GridCellPayload{
				X: x, Y: y,
				BlocksMovement:   cell.BlocksMovement,
				BlocksLOS:        cell.BlocksLOS,
				DifficultTerrain: cell.DifficultTerrain,
			})
		}
	}

	visibleTokens := combatmap.VisibleTokens(meta.state.Tokens, visible)
	tokenPayloads := make([]protocol.TokenPayload, len(visibleTokens))
	renderTokens := make([]combatmap.RenderToken, len(visibleTokens))
	for i, tok := range visibleTokens {
		tokenPayloads[i] = protocol.TokenPayload{
			TokenID: tok.TokenID, CharacterID: tok.CharacterID,
			Position: protocol.GridPositionPayload{X: tok.X, Y: tok.Y},
		}
		renderTokens[i] = combatmap.RenderToken{X: tok.X, Y: tok.Y, Color: meta.colors[tok.CharacterID]}
	}

	imageURL, err := combatmap.Render(grid, visible, renderTokens)
	if err != nil {
		return fmt.Errorf("rendering combat map: %w", err)
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeMapTokenState, protocol.MapTokenStatePayload{
		RoomID: campaignID, // no real room-graph concept yet — see internal/combatmap's package doc comment; one grid per campaign's active combat today
		Grid: protocol.MapGridPayload{
			Width: grid.Width, Height: grid.Height,
			Cells: cells,
		},
		Tokens:   tokenPayloads,
		ImageURL: imageURL,
	})
	if err != nil {
		return err
	}
	return sendToSender(s, recipient, msg)
}

// sendToSender marshals msg and delivers it only to sender's own
// connection(s) within msg.CampaignID via s.hub.SendToSender — the
// per-recipient counterpart to broadcastMessage, for a message whose
// content legitimately differs per recipient (map.token_state's fog of
// war). Deliberately does not call recordEvent: the shared campaign event
// log has one slot per message, and different recipients legitimately
// receiving different content for "the same" event has nowhere honest to
// go in a single shared history stream — the same design doc §9.7
// Knowledge Scoping gap sendCharacterState's own doc comment already
// flags as not decided yet, applied here the same way.
func sendToSender[T any](s *Server, sender string, msg protocol.Message[T]) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", msg.Type, err)
	}
	s.hub.SendToSender(msg.CampaignID, sender, payload)
	return nil
}

// handleMapTokenMoveRequest processes a map.token_move_request: validates
// the sender owns the character the token belongs to (ownedCharacter,
// same gate roll.check_request/character.apply_effect already use),
// validates the move mechanically (combatmap.ValidateMove — speed and
// the blocking grid, see that function's doc comment for the diagonal-
// movement simplification), mutates position, and re-sends every
// affected owner's own map.token_state — moving can change what more
// than one player can see (stepping past a corner can reveal or hide
// something for someone else too), so this recomputes for everyone with
// a token on the map, not just the mover. Owner/color metadata is never
// recomputed here — it's decided once at generation (combatMapMeta) and
// stays stable for the rest of the encounter.
//
// Unlike a DM tool call, there is no separate explicit "ok" reply on
// success — the resulting map.token_state send (to the mover and anyone
// else affected) is itself the substantive response, the same pattern
// character.apply_effect's dispatch case already uses (its real response
// is the character.state that follows, not a bare acknowledgement).
func (s *Server) handleMapTokenMoveRequest(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.MapTokenMoveRequestMessage) error {
	s.combatMapsMu.Lock()
	meta, ok := s.combatMaps[campaignID]
	s.combatMapsMu.Unlock()
	if !ok {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("no combat map is active for this campaign"))
	}

	tok, ok := meta.state.TokenByID(req.Payload.TokenID)
	if !ok {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("unknown token %q", req.Payload.TokenID))
	}

	character, err := s.ownedCharacter(ctx, campaignID, senderID, tok.CharacterID, "move")
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}
	info := parseCombatInfo(character.CharacterData)

	valid, _ := combatmap.ValidateMove(meta.state.Grid, tok.X, tok.Y, req.Payload.To.X, req.Payload.To.Y, info.speedFeet())
	if !valid {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("move rejected: destination is out of movement range, blocked, or unreachable"))
	}

	meta.state.MoveToken(tok.TokenID, req.Payload.To.X, req.Payload.To.Y)
	s.broadcastCombatMapToEveryOwner(campaignID, meta)
	return nil
}

// buildGridContext returns real position/blocking-cell data for
// dmCastSpell's CastSpellRequest (protocol/system_engine.proto's
// grid_context, see its own doc comment) — set only when campaignID has
// an active combat map and both casterCharacterID and targetCharacterID
// have a token on it, so OpenCombatEngine's already-tested range/line-
// of-sight logic (CastSpellAction.Execute) actually receives real data
// to check for the first time; returns nil otherwise (no combat map
// generated, or either character has no token on it), which leaves
// CastSpell's own context.Grid null and every existing no-grid cast
// behaving exactly as it always has.
func (s *Server) buildGridContext(campaignID, casterCharacterID, targetCharacterID string) *systemenginepb.GridContext {
	s.combatMapsMu.Lock()
	meta, ok := s.combatMaps[campaignID]
	s.combatMapsMu.Unlock()
	if !ok {
		return nil
	}

	casterToken, ok := meta.state.TokenByCharacterID(casterCharacterID)
	if !ok {
		return nil
	}
	targetToken, ok := meta.state.TokenByCharacterID(targetCharacterID)
	if !ok {
		return nil
	}

	grid := meta.state.Grid
	var obstacles []*systemenginepb.GridPosition
	for y := 0; y < grid.Height; y++ {
		for x := 0; x < grid.Width; x++ {
			cell, _ := grid.At(x, y)
			if cell.BlocksLOS {
				obstacles = append(obstacles, &systemenginepb.GridPosition{X: int32(x), Y: int32(y)})
			}
		}
	}

	return &systemenginepb.GridContext{
		CasterPosition: &systemenginepb.GridPosition{X: int32(casterToken.X), Y: int32(casterToken.Y)},
		TargetPosition: &systemenginepb.GridPosition{X: int32(targetToken.X), Y: int32(targetToken.Y)},
		Obstacles:      obstacles,
	}
}
