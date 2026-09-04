// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package server implements Master's client-facing WebSocket endpoint:
// the protocol handshake (system.connect, optionally gated by package
// auth -> system.session_state, or system.error on rejection), followed
// by a per-connection message loop that dispatches whatever the client
// sends next. See design doc §5 for the protocol and §11 for what's
// still to come. Implemented so far, by area:
//
//   - safety.flag (§9.2): broadcast to the campaign via package
//     session's Hub.
//   - log.history_request (§10, §11): answered from package store.
//   - narrative.player_input (§7): the fast pass (see renderPlayerBubble)
//     renders and broadcasts narrative.player_bubble synchronously, then
//     launches the slow pass (see runSlowPass) in its own goroutine —
//     the DM/NPC reaction, using design doc §8's DM tool-use pattern
//     (resolve_check/apply_effect/get_character_status, see dm_tools.go)
//     to resolve mechanical uncertainty rather than inventing outcomes,
//     broadcasting a tool.result per call and the final reaction as
//     narrative.dm_prose. Neither pass is fed campaign/character context
//     beyond the player's own input yet — no persistent context-assembly
//     exists in Master to feed it.
//   - character.upload (§9.4's mechanical half only — see
//     importCharacter): validated via package systemenginepb, answered
//     with character.validation_result. The human review/veto half
//     (character.review_status, pending_review -> approved/rejected) is
//     NOT implemented — it needs a privileged-operator concept this
//     codebase doesn't have yet.
//   - roll.check_request (see resolveCheck): calls ResolveCheck for a
//     character the sender owns, broadcasts roll.request/roll.result.
//     roll.acknowledge is NOT implemented (no narration-sequencing
//     pipeline exists yet to feed).
//   - character.schema_request/character.get (see sendCharacterSchema/
//     sendCharacterState): forwards GetCharacterSchema, and answers with
//     a sender-owned character's data plus GetCharacterStatus.
//   - character.apply_effect (see applyCharacterEffect): calls
//     ApplyEffect for a character the sender owns, persists the result,
//     answers privately with character.state — not broadcast, since
//     effect visibility is design doc §9.7 Knowledge Scoping territory,
//     not decided yet.
//   - The turn-order state machine (§3.1, §9.3, see turn_order.go):
//     start_combat/advance_turn/end_combat DM tools (dm_tools.go) drive
//     it, but the mechanical bookkeeping — initiative order from real
//     Dexterity checks, skipping only dead characters — is Master's own,
//     independent of the DM model's judgment. Landing a turn on any
//     non-dead character (startTurnFor) calls the System Engine's
//     StartTurn, which automatically rolls a death save for an
//     unconscious/dying character (SRD's own rule, real and broadcast as
//     a genuine roll.result — never invented) rather than skipping their
//     turn outright. Broadcasts turn.state. In-memory only; doesn't
//     survive a Master restart. Once combat is active, enforceTurnOrder
//     rejects a player's own roll.check_request/character.apply_effect
//     unless it's that character's turn — the DM's own tool calls are
//     deliberately not gated this way (see enforceTurnOrder's doc
//     comment for why).
//   - Governance gates (§9, see package policy and campaignPolicy):
//     §9.1's PvP policy is a real mechanical gate in dmApplyEffect — a
//     hostile apply_effect against a different player's character is
//     blocked outright unless the campaign's configured policy permits
//     it, never left to the DM model to self-police. §9.5's maturity
//     tier is (by design doc's own description) prompting-only, not a
//     hard filter — an operator-authored constraint string appended to
//     both narrative passes' system prompts when configured. Both
//     resolve from a flat JSON file (design doc §6.6's precedent for a
//     per-campaign operator setting), not yet §6.4's full campaign-pack
//     directory tree — see package policy's JSONFileProvider doc
//     comment. §9.2 (safety.flag) predates this and lives separately;
//     the rest of §9 (death/turn handling is §9.3, already covered
//     above; §9.4/§9.6/§9.7/§9.8) is still to come.
//   - Image generation (§6.3, see package imagegen and
//     dmGenerateSceneImage in dm_tools.go): the generate_scene_image DM
//     tool calls a pluggable imagegen.Provider (a self-hosted ComfyUI
//     instance is the reference implementation) and broadcasts the
//     result as narrative.scene_image. Not offered as a DM tool at all
//     when no provider is configured (s.imageGen == nil), same pattern
//     as tools requiring a system engine.
//   - The combat map (§6.2, see internal/combatmap and combat_map.go):
//     an opt-in generate_combat_map DM tool generates a grid, auto-places
//     tokens, and sends each connected player their own per-character fog
//     of war (recursive shadowcasting against the generated blocking
//     grid) as map.token_state — never Broadcast, since two players
//     legitimately see different things for the same event
//     (session.Hub.SendToSender). map.token_move_request validates
//     ownership, movement speed, and the blocking grid before accepting.
//     Grid/position data does not reach the System Engine in this
//     version — see internal/combatmap's package doc comment for why
//     mechanically gating spell/attack range/line-of-sight/cover against
//     it is separate, deferred work.
//
// See CLAUDE.md and each dispatch case's own comments for why a given
// message either is or isn't implemented yet.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/imagegen"
	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// masterSenderID is the sender_id Master uses on messages it originates
// (session-state notifications, errors, and broadcasts), since none of
// them are attributed to a specific human sender — see
// broadcastSafetyFlag for why that matters for safety.flag specifically.
const masterSenderID = "master"

// Server serves Master's client-facing WebSocket endpoint.
type Server struct {
	logger     *slog.Logger
	events     store.EventStore
	characters store.CharacterStore
	hub        *session.Hub
	auth       auth.Provider

	llm            llm.Provider
	narrativeModel string

	systemEngine systemenginepb.SystemEngineClient

	// policy resolves each campaign's governance settings (design doc §9
	// — see package policy). May be nil to apply policy.Default() (the
	// strictest PvP setting, no maturity constraint) to every campaign,
	// same nil-means-disabled pattern as auth/systemEngine above.
	policy policy.Provider

	// imageGen generates scene illustrations (design doc §6.3, see
	// dmGenerateSceneImage in dm_tools.go). May be nil to run without
	// image generation at all — the generate_scene_image DM tool is then
	// simply not offered, same "nil means the feature doesn't exist for
	// this deployment" pattern as llm/systemEngine.
	imageGen imagegen.Provider

	// turnOrders holds each campaign's live turn-order state (turn_order.go,
	// design doc §3.1, §9.3), guarded by turnOrdersMu since it's mutated
	// from whichever goroutine is running a DM tool call at the time —
	// the same concurrency shape session.Hub's own connection registry
	// has, kept local here since nothing outside this package touches it.
	turnOrders   map[string]*turnOrder
	turnOrdersMu sync.Mutex

	// combatMaps holds each campaign's active combat-map state
	// (combat_map.go, internal/combatmap, design doc §6.2) — same
	// in-memory-only, guarded-by-its-own-mutex shape as turnOrders, and
	// the same documented "lost on Master restart" limitation. Unlike
	// turnOrders, a campaign with no map generated for its current combat
	// simply has no entry here; this is always optional, never required
	// for combat to function (see dmGenerateCombatMap's own doc comment
	// for why it's a separate opt-in DM tool rather than automatic).
	combatMaps   map[string]*combatMapMeta
	combatMapsMu sync.Mutex

	// combatState persists turnOrders/combatMaps so they survive a
	// Master restart (combat_state.go) — nil runs exactly as before this
	// existed (in-memory only, lost on restart), the same "nil disables
	// the feature" pattern every other optional dependency on this
	// struct already uses.
	combatState store.CombatStateStore

	// campaignPack persists which campaign-pack directory (if any) is
	// bound to each campaign, plus the mutable session state a loaded
	// pack's locations need — party location, discovered/claimed
	// locations, stashed possessions (location.go, design doc §6.4).
	// nil means no campaign ever has a pack bound — the location_*/
	// stash_* DM tools then simply reject every call with a real "no
	// campaign pack is bound to this campaign" error, the same
	// nil-disables-the-feature pattern as combatState/imageGen/policy.
	campaignPack store.CampaignPackStore

	// vehicles persists real mounts/carts/wagons/ships (vehicles.go,
	// design doc §6.4's "off-site possessions (mounts, stashes)" —
	// stashes' other named half). nil means vehicle tools always reject
	// with a real "not configured" error, the same nil-disables-the-
	// feature pattern as campaignPack.
	vehicles store.VehicleStore
}

// New creates a Server. logger must not be nil; pass slog.Default() if
// the caller has no specific logger configured. events may be nil to run
// without persistence (e.g. in tests that don't care about the audit
// log) — every message Server exchanges is still handled normally either
// way, since recording is best-effort (see recordEvent). llmProvider may
// also be nil to run without narrative rendering — a
// narrative.player_input then gets a system.error explaining rendering
// is unavailable, rather than Master panicking on a nil provider.
// narrativeModel names the model llmProvider should use; it's ignored
// when llmProvider is nil. authProvider may be nil to run with no join
// authorization at all (every campaign open to anyone, today's default);
// design doc §6.6 — a future Discord-OAuth-backed auth.Provider is meant
// to plug into this same field. systemEngineClient may be nil to run
// without a System Engine sidecar configured at all (design doc §6.1);
// character.upload then gets a system.error explaining import is
// unavailable, same as narrative.player_input does without an llmProvider.
// characterStore may independently be nil to run without character
// persistence at all, same reasoning as events being nil. policyProvider
// may be nil to apply policy.Default() to every campaign (design doc §9
// — see package policy and campaignPolicy). imageGenProvider may be nil
// to run without image generation at all (design doc §6.3) — the
// generate_scene_image DM tool is then simply not offered. combatStateStore
// may be nil to run with turn-order/combat-map state in-memory only, the
// same "lost on Master restart" limitation this codebase had before
// combat_state.go existed — a caller that sets it should also call
// WarmUpCombatState once at startup, before Handler() starts accepting
// connections, to rehydrate whatever was persisted from a prior run.
// campaignPackStore/vehicleStore may independently be nil to run without
// campaign-pack loading / vehicle tracking at all — the location_*/
// stash_*/vehicle_* DM tools then simply aren't offered (see
// dm_slow_pass.go's tool-assembly gate).
func New(logger *slog.Logger, events store.EventStore, llmProvider llm.Provider, narrativeModel string, authProvider auth.Provider, systemEngineClient systemenginepb.SystemEngineClient, characterStore store.CharacterStore, policyProvider policy.Provider, imageGenProvider imagegen.Provider, combatStateStore store.CombatStateStore, campaignPackStore store.CampaignPackStore, vehicleStore store.VehicleStore) *Server {
	return &Server{
		logger:         logger,
		events:         events,
		characters:     characterStore,
		hub:            session.NewHub(),
		llm:            llmProvider,
		narrativeModel: narrativeModel,
		auth:           authProvider,
		systemEngine:   systemEngineClient,
		policy:         policyProvider,
		imageGen:       imageGenProvider,
		turnOrders:     make(map[string]*turnOrder),
		combatMaps:     make(map[string]*combatMapMeta),
		combatState:    combatStateStore,
		campaignPack:   campaignPackStore,
		vehicles:       vehicleStore,
	}
}

// campaignPolicy resolves campaignID's governance policy (design doc
// §9), falling back to policy.Default() — the strictest PvP setting and
// no maturity constraint — both when no policy.Provider is configured at
// all (s.policy == nil) and when a configured Provider itself errors,
// since a governance gate should fail closed, not open.
func (s *Server) campaignPolicy(ctx context.Context, campaignID string) policy.CampaignPolicy {
	if s.policy == nil {
		return policy.Default()
	}
	pol, err := s.policy.Policy(ctx, campaignID)
	if err != nil {
		s.logger.Warn("failed to resolve campaign policy, falling back to the safe default", "error", err, "campaign_id", campaignID)
		return policy.Default()
	}
	return pol
}

// withMaturityConstraint appends pol's maturity-tier prompt constraint
// (design doc §9.5, §6.5) to basePrompt when one is configured — shared
// by both narrative passes (renderPlayerBubble's fast pass and
// runSlowPass's slow pass), since §9.5 governs "DM text generation"
// broadly, not just the DM's own reaction. Returns basePrompt unchanged
// when pol.MaturityTierPrompt is empty, matching this codebase's
// behavior before campaign policy existed.
func withMaturityConstraint(basePrompt string, pol policy.CampaignPolicy) string {
	if pol.MaturityTierPrompt == "" {
		return basePrompt
	}
	return basePrompt + "\n\nContent guidance for this table: " + pol.MaturityTierPrompt
}

// Handler returns the http.Handler that serves the WebSocket endpoint,
// suitable for mounting at any path (e.g. "/ws") on an http.ServeMux.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handleWebSocket)
}

// handleWebSocket upgrades r to a WebSocket connection and runs the
// protocol handshake on it. Errors are logged, not returned — an
// http.Handler has no caller to report them to once the upgrade
// succeeds.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Error("websocket accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	if err := s.handleConnection(r.Context(), conn); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("connection ended with error", "error", err)
	}
}

// handleConnection runs one client connection: the protocol handshake,
// then (if it succeeds) the post-handshake message loop in serve. A
// panic anywhere in that call chain (e.g. from a pathological message)
// is recovered here so it can't take Master down for every other player
// at the table — see CLAUDE.md's error-handling conventions for why this
// is the one place a recover() belongs. Note this only covers the
// goroutine handleConnection itself runs on; serve's write pump runs on
// its own goroutine and recovers separately (see serve).
func (s *Server) handleConnection(ctx context.Context, conn *websocket.Conn) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic handling connection: %v", r)
		}
	}()

	var connect protocol.SystemConnectMessage
	if err := wsjson.Read(ctx, conn, &connect); err != nil {
		return fmt.Errorf("reading handshake: %w", err)
	}
	recordEvent(ctx, s, connect)

	campaignID := connect.CampaignID
	if campaignID == "" {
		campaignID = "unknown"
	}

	if verr := connect.Envelope.Validate(); verr != nil {
		return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID, verr)
	}
	if connect.Type != protocol.MessageTypeSystemConnect {
		return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
			fmt.Errorf("first message must be %q, got %q", protocol.MessageTypeSystemConnect, connect.Type))
	}

	if s.auth != nil {
		authorized, reason, authErr := s.auth.Authorize(ctx, campaignID, connect.Payload.AuthToken)
		if authErr != nil {
			return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
				fmt.Errorf("checking authorization: %w", authErr))
		}
		if !authorized {
			return s.rejectHandshake(ctx, conn, connect.MessageID, campaignID,
				fmt.Errorf("not authorized to join this campaign: %s", reason))
		}
	}

	s.logger.Info("client connected",
		"client_kind", connect.Payload.ClientKind,
		"campaign_id", connect.CampaignID,
	)

	if err := s.sendSessionState(ctx, conn, campaignID, protocol.SessionStateJoined); err != nil {
		return err
	}

	return s.serve(ctx, conn, campaignID, connect.SenderID)
}

// sendSessionState sends a system.session_state message to conn.
func (s *Server) sendSessionState(ctx context.Context, conn *websocket.Conn, campaignID string, state protocol.SessionState) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemSessionState, protocol.SystemSessionStatePayload{
		State: state,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing session_state: %w", err)
	}
	return nil
}

// serve runs the post-handshake message loop for a joined connection: a
// write pump delivering session.Hub broadcasts to this client, and a
// read loop dispatching each inbound message by type. It returns once
// the connection ends, for any reason — client disconnect, a read/write
// error, or ctx cancellation. senderID is the sender_id this connection
// authenticated as at handshake time (its system.connect message) —
// registered with the Hub so a later Hub.SendToSender can target this
// connection specifically (design doc §9's per-player fog-of-war sends,
// internal/server/combat_map.go).
func (s *Server) serve(ctx context.Context, conn *websocket.Conn, campaignID, senderID string) error {
	client := s.hub.Register(campaignID, senderID)
	defer s.hub.Unregister(client)

	writeDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeDone <- fmt.Errorf("recovered from panic in write pump: %v", r)
			}
		}()
		writeDone <- s.writePump(ctx, conn, client)
	}()

	readErr := s.readLoop(ctx, conn, campaignID)

	// Unregister (above, via defer) closes client's outbox once serve
	// returns, which ends writePump's range loop — but that hasn't run
	// yet at this point, so wait for it now rather than returning (and
	// letting the caller close conn) while it might still be writing.
	writeErr := <-writeDone

	if readErr != nil {
		return readErr
	}
	return writeErr
}

// writePump delivers every message sent to client's outbox to conn,
// until the outbox is closed (by Hub.Unregister) or a write fails.
func (s *Server) writePump(ctx context.Context, conn *websocket.Conn, client *session.Client) error {
	for payload := range client.Outbox() {
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return fmt.Errorf("writing broadcast message: %w", err)
		}
	}
	return nil
}

// readLoop reads messages from conn until one read fails (including the
// client disconnecting, or ctx being canceled), dispatching each to
// dispatch. dispatch itself only returns an error for a genuine
// transport failure while responding to the sender — a message that's
// merely malformed or unsupported gets a system.error reply and the loop
// continues, so one bad message doesn't end the connection.
func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn, campaignID string) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading message: %w", err)
		}
		if err := s.dispatch(ctx, conn, campaignID, data); err != nil {
			return err
		}
	}
}

// dispatch decodes one inbound message and routes it by type.
func (s *Server) dispatch(ctx context.Context, conn *websocket.Conn, campaignID string, data []byte) error {
	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("malformed message: %w", err))
	}
	if verr := envelope.Validate(); verr != nil {
		return s.sendError(ctx, conn, campaignID, envelope.MessageID, verr)
	}

	switch envelope.Type {
	case protocol.MessageTypeSafetyFlag:
		var flag protocol.SafetyFlagMessage
		if err := json.Unmarshal(data, &flag); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed safety.flag payload: %w", err))
		}
		recordEvent(ctx, s, flag)
		return s.broadcastSafetyFlag(ctx, campaignID, flag.Payload.Topic)
	case protocol.MessageTypeLogHistoryRequest:
		var req protocol.HistoryRequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed log.history_request payload: %w", err))
		}
		// Not recorded to the event log: this is a query against the
		// log, not a game event — recording it would just be recursive
		// noise (a request to view history, sitting in the history).
		return s.sendHistory(ctx, conn, campaignID, envelope.MessageID, req.Payload)
	case protocol.MessageTypeNarrativePlayerInput:
		var input protocol.NarrativePlayerInputMessage
		if err := json.Unmarshal(data, &input); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed narrative.player_input payload: %w", err))
		}
		recordEvent(ctx, s, input)
		return s.renderPlayerBubble(ctx, conn, campaignID, input)
	case protocol.MessageTypeCharacterUpload:
		var upload protocol.CharacterUploadMessage
		if err := json.Unmarshal(data, &upload); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed character.upload payload: %w", err))
		}
		recordEvent(ctx, s, upload)
		return s.importCharacter(ctx, conn, campaignID, envelope.SenderID, upload)
	case protocol.MessageTypeRollCheckRequest:
		var req protocol.RollCheckRequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed roll.check_request payload: %w", err))
		}
		recordEvent(ctx, s, req)
		return s.resolveCheck(ctx, conn, campaignID, envelope.SenderID, req)
	case protocol.MessageTypeCharacterSchemaRequest:
		var req protocol.CharacterSchemaRequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed character.schema_request payload: %w", err))
		}
		// Not recorded: a schema fetch is a query, not a game event, same
		// reasoning as log.history_request.
		return s.sendCharacterSchema(ctx, conn, campaignID, req.MessageID)
	case protocol.MessageTypeCharacterGet:
		var req protocol.CharacterGetMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed character.get payload: %w", err))
		}
		return s.sendCharacterState(ctx, conn, campaignID, envelope.SenderID, req)
	case protocol.MessageTypeCharacterApplyEffect:
		var req protocol.CharacterApplyEffectMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed character.apply_effect payload: %w", err))
		}
		recordEvent(ctx, s, req)
		return s.applyCharacterEffect(ctx, conn, campaignID, envelope.SenderID, req)
	case protocol.MessageTypeMapTokenMoveRequest:
		var req protocol.MapTokenMoveRequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed map.token_move_request payload: %w", err))
		}
		// Not recorded to the event log: the resulting map.token_state
		// sends are themselves per-recipient and already skip recordEvent
		// for the same reason (see sendToSender's doc comment) — logging
		// the request but not its differently-shaped-per-recipient result
		// would be a misleading half-record.
		return s.handleMapTokenMoveRequest(ctx, conn, campaignID, envelope.SenderID, req)
	case protocol.MessageTypeVehicleImport:
		var req protocol.VehicleImportMessage
		if err := json.Unmarshal(data, &req); err != nil {
			return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("malformed vehicle.import payload: %w", err))
		}
		recordEvent(ctx, s, req)
		return s.importVehicle(ctx, conn, campaignID, req)
	default:
		return s.sendError(ctx, conn, campaignID, envelope.MessageID, fmt.Errorf("unsupported message type %q", envelope.Type))
	}
}

// broadcastSafetyFlag builds and persists a safety.flag_broadcast
// message and delivers it to every client currently registered under
// campaignID — deliberately including whichever client sent the
// triggering safety.flag, since design doc §9.2 wants the scene
// interrupted for everyone at the table, sender included, and
// deliberately not naming who sent it: the broadcast is attributed to
// masterSenderID like every other Master-originated message, not to the
// flagging client.
func (s *Server) broadcastSafetyFlag(ctx context.Context, campaignID, topic string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSafetyFlagBroadcast, protocol.SafetyFlagBroadcastPayload{
		Topic: topic,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	return broadcastMessage(s, msg)
}

// narrativeFastPassSystemPrompt instructs the model for design doc §7's
// fast pass: render the player's stated action/dialogue in third-person
// prose, faithfully — not the DM/NPC reaction, and not a ruling on
// whether the action succeeds. That's the slow pass's job (design doc
// §7's second beat), which isn't implemented — see renderPlayerBubble.
const narrativeFastPassSystemPrompt = `You are rendering a tabletop RPG player's stated action or dialogue into brief, third-person, present-tense narrative prose for a shared chat log.
Rules:
- Describe only what the player explicitly stated — do not invent new events, dialogue, or outcomes.
- Do not resolve success or failure of any action; that is decided elsewhere.
- Keep it to 1-3 sentences.
- Write in the tone of a dungeon master narrating at the table.
- Output only the narrated prose, nothing else — no preamble, no quotation marks around it.`

// renderPlayerBubble runs the narrative-transform pipeline's fast pass
// (design doc §7): rendering the player's stated action/dialogue in
// third-person DM-voiced prose via s.llm, then broadcasting the result as
// narrative.player_bubble to everyone in the campaign. Once that
// succeeds, it launches the slow pass (see runSlowPass) in its own
// detached goroutine — the DM/NPC reaction, including any tool calls
// (design doc §8), can take much longer than the fast pass and must not
// block this connection's read loop from handling the player's next
// message while it runs. No campaign/character context beyond the
// player's own input is fed to either pass, since no persistent
// campaign-context assembly exists in Master yet to feed it.
func (s *Server) renderPlayerBubble(ctx context.Context, conn *websocket.Conn, campaignID string, input protocol.NarrativePlayerInputMessage) error {
	if s.llm == nil {
		return s.sendError(ctx, conn, campaignID, input.MessageID, errors.New("narrative rendering unavailable: no LLM provider configured"))
	}

	completion, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Model:        s.narrativeModel,
		SystemPrompt: withMaturityConstraint(narrativeFastPassSystemPrompt, s.campaignPolicy(ctx, campaignID)),
		UserPrompt:   input.Payload.Text,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, input.MessageID, fmt.Errorf("rendering narrative: %w", err))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeNarrativePlayerBubble, protocol.NarrativePlayerBubblePayload{
		CharacterID: input.Payload.CharacterID,
		Text:        completion.Text,
		Editable:    true,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := broadcastMessage(s, msg); err != nil {
		return err
	}

	go s.runSlowPass(campaignID, input)
	return nil
}

// importCharacter handles a character.upload message: it asks the System
// Engine sidecar to parse+mechanically-validate upload's character JSON
// (design doc §9.4), persists a successfully-parsed character as
// store.CharacterStatusPendingReview, and replies with
// character.validation_result carrying whatever warnings the engine
// returned.
//
// It deliberately never sets a character's status to Approved — design
// doc §9.4's veto/review panel needs a privileged-operator concept Master
// doesn't have yet (no account/role system exists, only room-password
// join auth), and approving on Master's own say-so instead would violate
// CLAUDE.md's "gates over prompting" rule rather than satisfy it. A
// character that parses successfully still lands in PendingReview, same
// as one with mechanical warnings — this endpoint only proves the upload
// is well-formed, not that a human has reviewed it.
func (s *Server) importCharacter(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, upload protocol.CharacterUploadMessage) error {
	if s.systemEngine == nil {
		return s.sendError(ctx, conn, campaignID, upload.MessageID, errors.New("character import unavailable: no system engine configured"))
	}
	if s.characters == nil {
		return s.sendError(ctx, conn, campaignID, upload.MessageID, errors.New("character import unavailable: character storage is disabled"))
	}

	resp, err := s.systemEngine.FromJson(ctx, &systemenginepb.FromJsonRequest{Json: upload.Payload.CharacterJSON})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, upload.MessageID, fmt.Errorf("calling system engine FromJson: %w", err))
	}

	warnings := make([]protocol.CharacterValidationWarning, len(resp.Warnings))
	for i, w := range resp.Warnings {
		warnings[i] = protocol.CharacterValidationWarning{FieldPath: w.FieldPath, Message: w.Message, Severity: w.Severity}
	}

	var characterID string
	if resp.Actor != nil {
		characterData, err := protojson.Marshal(resp.Actor.CharacterData)
		if err != nil {
			return s.sendError(ctx, conn, campaignID, upload.MessageID, fmt.Errorf("marshaling parsed character data: %w", err))
		}

		characterID, err = newCharacterID()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := s.characters.SaveCharacter(ctx, store.Character{
			ID:            characterID,
			CampaignID:    campaignID,
			OwnerID:       senderID,
			SchemaVersion: resp.Actor.SchemaVersion,
			Status:        store.CharacterStatusPendingReview,
			CharacterData: characterData,
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return s.sendError(ctx, conn, campaignID, upload.MessageID, fmt.Errorf("saving character: %w", err))
		}
	}
	// resp.Actor is nil when FromJson couldn't parse upload.Payload.
	// CharacterJSON at all (see the sidecar's own FromJson: it populates
	// only Warnings, not Actor, on a parse failure) — nothing is saved in
	// that case, and characterID stays "" so the client can tell no
	// character record was created.

	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterValidationResult, protocol.CharacterValidationResultPayload{
		CharacterID: characterID,
		Warnings:    warnings,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.validation_result: %w", err)
	}
	return nil
}

// resolveCheck handles a roll.check_request message: it looks up
// req.Payload.CharacterID (rejecting the request if senderID doesn't own
// that character — design doc §9.4's OwnerID is the only ownership
// concept this codebase has, but it's real and enforced here, not
// aspirational), rejects it if structured combat is active and it isn't
// that character's turn (enforceTurnOrder, design doc §3.1, §9.3), calls
// the System Engine's ResolveCheck for it, and broadcasts the outcome to
// the whole campaign as roll.request (so every client's dice tray can
// pre-stage an animation) followed by roll.result (the authoritative
// outcome, design doc §3.1, §4) — never just to the requester, since
// design doc §4's dice tray is meant to be a shared, visible-to-everyone
// table event, not a private roll.
//
// roll.request's RollSpec is derived from the real, already-resolved
// Outcome.Rolls (grouped by die size), not assumed — Master never
// hardcodes which dice a system engine uses (design doc §6.1, CLAUDE.md).
func (s *Server) resolveCheck(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.RollCheckRequestMessage) error {
	if s.systemEngine == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("dice resolution unavailable: no system engine configured"))
	}
	if s.characters == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("dice resolution unavailable: character storage is disabled"))
	}

	character, err := s.ownedCharacter(ctx, campaignID, senderID, req.Payload.CharacterID, "roll checks for")
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}
	if err := s.enforceTurnOrder(campaignID, character.ID); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("parsing stored character data: %w", err))
	}

	paramFields := map[string]any{"checkType": req.Payload.CheckType}
	if req.Payload.Ability != "" {
		paramFields["ability"] = req.Payload.Ability
	}
	if req.Payload.Skill != "" {
		paramFields["skill"] = req.Payload.Skill
	}
	params, err := structpb.NewStruct(paramFields)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("building check params: %w", err))
	}

	resp, err := s.systemEngine.ResolveCheck(ctx, &systemenginepb.ResolveCheckRequest{
		RequestId:  req.MessageID,
		CampaignId: campaignID,
		Actor: &systemenginepb.Actor{
			ActorId:       character.ID,
			CharacterData: characterData,
			SchemaVersion: character.SchemaVersion,
		},
		Params: params,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("calling system engine ResolveCheck: %w", err))
	}
	if !resp.Success {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("check could not be resolved: %s", resp.Error))
	}

	return s.broadcastRollOutcome(ctx, campaignID, character.ID, resp.Outcome)
}

// broadcastRollOutcome announces a resolved check to every client in
// campaignID as roll.request (so a dice tray can pre-stage an animation,
// RollSpec derived from outcome.Rolls grouped by die size — never
// assumed, Master doesn't hardcode which dice a system engine uses,
// design doc §6.1, CLAUDE.md) followed by roll.result (the authoritative
// outcome, design doc §3.1, §4). Shared by resolveCheck (a player's own
// roll.check_request) and the DM tool-use resolve_check tool (design doc
// §8) — a DM-triggered check is just as much a shared table event as a
// player-triggered one, so both animate the same way.
func (s *Server) broadcastRollOutcome(ctx context.Context, campaignID, characterID string, outcome *systemenginepb.Outcome) error {
	rolls := make([]protocol.DieRoll, len(outcome.Rolls))
	var diceOrder []int
	diceCounts := make(map[int]int)
	for i, r := range outcome.Rolls {
		rolls[i] = protocol.DieRoll{Sides: int(r.Sides), Result: int(r.Result), Label: r.Label}
		sides := int(r.Sides)
		if _, seen := diceCounts[sides]; !seen {
			diceOrder = append(diceOrder, sides)
		}
		diceCounts[sides]++
	}
	dice := make([]protocol.RollDie, len(diceOrder))
	for i, sides := range diceOrder {
		dice[i] = protocol.RollDie{Sides: sides, Count: diceCounts[sides]}
	}

	requestMsg, err := newMessage(campaignID, protocol.MessageTypeRollRequest, protocol.RollRequestPayload{
		CharacterID: characterID,
		RollSpec:    protocol.RollSpec{Dice: dice},
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, requestMsg)
	if err := broadcastMessage(s, requestMsg); err != nil {
		return err
	}

	resultMsg, err := newMessage(campaignID, protocol.MessageTypeRollResult, protocol.RollResultPayload{
		CharacterID:   characterID,
		Rolls:         rolls,
		Total:         int(outcome.Total),
		ResultSummary: outcome.ResultSummary,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, resultMsg)
	return broadcastMessage(s, resultMsg)
}

// sendCharacterSchema answers a character.schema_request with the active
// system engine's own get_character_schema() output, forwarded unchanged
// (design doc §4, §6.1) — schema-wide, not per-character, so callers
// fetch it once and reuse it for every character sheet they render.
func (s *Server) sendCharacterSchema(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string) error {
	if s.systemEngine == nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("character schema unavailable: no system engine configured"))
	}

	resp, err := s.systemEngine.GetCharacterSchema(ctx, &systemenginepb.GetCharacterSchemaRequest{})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("calling system engine GetCharacterSchema: %w", err))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterSchemaResponse, protocol.CharacterSchemaResponsePayload{
		SchemaVersion: resp.SchemaVersion,
		JSONSchema:    resp.JsonSchema,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.schema_response: %w", err)
	}
	return nil
}

// sendCharacterState answers a character.get with a previously-uploaded
// character's current data and mechanical status (design doc §9.3's
// get_character_status(), not something Master infers itself). Rejects
// the request if senderID doesn't own that character — the same
// ownership gate resolveCheck uses, store.Character.OwnerID.
func (s *Server) sendCharacterState(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.CharacterGetMessage) error {
	if s.systemEngine == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("character state unavailable: no system engine configured"))
	}
	if s.characters == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("character state unavailable: character storage is disabled"))
	}

	character, err := s.ownedCharacter(ctx, campaignID, senderID, req.Payload.CharacterID, "view")
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("parsing stored character data: %w", err))
	}

	statusResp, err := s.systemEngine.GetCharacterStatus(ctx, &systemenginepb.GetCharacterStatusRequest{
		Actor: &systemenginepb.Actor{
			ActorId:       character.ID,
			CharacterData: characterData,
			SchemaVersion: character.SchemaVersion,
		},
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("calling system engine GetCharacterStatus: %w", err))
	}
	status, ok := characterStatusString(statusResp.Status)
	if !ok {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("system engine returned an unrecognized character status: %v", statusResp.Status))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterState, protocol.CharacterStatePayload{
		CharacterID:   character.ID,
		SchemaVersion: character.SchemaVersion,
		CharacterData: character.CharacterData,
		Status:        status,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.state: %w", err)
	}
	return nil
}

// applyCharacterEffect handles a character.apply_effect message: applies
// an engine-defined effect (design doc §6.1's apply_effect()) to a
// character the sender owns — rejecting it if structured combat is
// active and it isn't that character's turn, same enforceTurnOrder gate
// resolveCheck uses (design doc §3.1, §9.3) — persists the resulting
// state, and replies privately with character.state — not broadcast to
// the campaign. Wider table-visible effect notifications are design doc
// §9.7 Knowledge Scoping territory, not decided yet, so this stays as
// private as character.get rather than guessing at a broadcast policy.
func (s *Server) applyCharacterEffect(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.CharacterApplyEffectMessage) error {
	if s.systemEngine == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("applying effects unavailable: no system engine configured"))
	}
	if s.characters == nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("applying effects unavailable: character storage is disabled"))
	}

	character, err := s.ownedCharacter(ctx, campaignID, senderID, req.Payload.CharacterID, "apply effects to")
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}
	if err := s.enforceTurnOrder(campaignID, character.ID); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, err)
	}

	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("parsing stored character data: %w", err))
	}

	effect := &structpb.Struct{}
	if err := protojson.Unmarshal(req.Payload.Effect, effect); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("parsing effect: %w", err))
	}

	resp, err := s.systemEngine.ApplyEffect(ctx, &systemenginepb.ApplyEffectRequest{
		RequestId:  req.MessageID,
		CampaignId: campaignID,
		Actor: &systemenginepb.Actor{
			ActorId:       character.ID,
			CharacterData: characterData,
			SchemaVersion: character.SchemaVersion,
		},
		Effect: effect,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("calling system engine ApplyEffect: %w", err))
	}
	if !resp.Success {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("effect could not be applied: %s", resp.Error))
	}

	newCharacterData, err := protojson.Marshal(resp.Actor.CharacterData)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("marshaling updated character data: %w", err))
	}
	character.CharacterData = newCharacterData
	character.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, character); err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("saving updated character: %w", err))
	}

	statusResp, err := s.systemEngine.GetCharacterStatus(ctx, &systemenginepb.GetCharacterStatusRequest{Actor: resp.Actor})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("calling system engine GetCharacterStatus: %w", err))
	}
	status, ok := characterStatusString(statusResp.Status)
	if !ok {
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("system engine returned an unrecognized character status: %v", statusResp.Status))
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterState, protocol.CharacterStatePayload{
		CharacterID:   character.ID,
		SchemaVersion: character.SchemaVersion,
		CharacterData: character.CharacterData,
		Status:        status,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.state: %w", err)
	}
	return nil
}

// characterStatusString maps the System Engine gRPC contract's
// CharacterStatus enum to the lowercase strings protocol/asyncapi.yaml's
// CharacterState schema declares ("active | unconscious | dying |
// dead") — deliberately narrower than the proto enum: CHARACTER_STATUS_
// UNSPECIFIED is the required proto3 zero value, not a real status
// (protocol/system_engine.proto's own comment on it), so it — and any
// future enum value this build doesn't know about — reports ok=false
// rather than forwarding a value the wire schema doesn't declare.
func characterStatusString(status systemenginepb.CharacterStatus) (value string, ok bool) {
	switch status {
	case systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE:
		return "active", true
	case systemenginepb.CharacterStatus_CHARACTER_STATUS_UNCONSCIOUS:
		return "unconscious", true
	case systemenginepb.CharacterStatus_CHARACTER_STATUS_DYING:
		return "dying", true
	case systemenginepb.CharacterStatus_CHARACTER_STATUS_DEAD:
		return "dead", true
	default:
		return "", false
	}
}

// ownedCharacter looks up characterID and verifies it belongs to
// campaignID and is owned by senderID — the ownership gate resolveCheck,
// sendCharacterState, and applyCharacterEffect all need (design doc
// §9.4's OwnerID is the only ownership concept this codebase has;
// s.characters must be non-nil, checked by each caller before this).
// verb names the action being denied in the returned error's text (e.g.
// "roll checks for", "view", "apply effects to"), so each caller's
// rejection reads naturally; the returned error is meant to go straight
// into a system.error.
func (s *Server) ownedCharacter(ctx context.Context, campaignID, senderID, characterID, verb string) (store.Character, error) {
	character, err := s.characters.GetCharacter(ctx, characterID)
	if err != nil {
		return store.Character{}, fmt.Errorf("looking up character: %w", err)
	}
	if character.CampaignID != campaignID {
		return store.Character{}, errors.New("character does not belong to this campaign")
	}
	if character.OwnerID != senderID {
		return store.Character{}, fmt.Errorf("you can only %s your own characters", verb)
	}
	return character, nil
}

// sendHistory answers a log.history_request with a page of campaign's
// recorded events (design doc §10, §11), sent only to the requesting
// connection — history is per-viewer to ask for, not broadcast. It
// rejects the request (via sendError) if persistence is disabled, since
// silently returning an empty page would look identical to "this
// campaign really has no history" rather than "this feature is off".
func (s *Server) sendHistory(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string, req protocol.HistoryRequestPayload) error {
	if s.events == nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, errors.New("history unavailable: persistence is disabled"))
	}
	if req.AfterSequence != 0 && req.BeforeSequence != 0 {
		return s.sendError(ctx, conn, campaignID, inReplyTo, store.ErrConflictingPagination)
	}

	events, hasMore, err := s.events.ListEvents(ctx, campaignID, store.ListEventsOptions{
		AfterSequence:  req.AfterSequence,
		BeforeSequence: req.BeforeSequence,
		Limit:          req.Limit,
	})
	if err != nil {
		return s.sendError(ctx, conn, campaignID, inReplyTo, fmt.Errorf("fetching history: %w", err))
	}

	raw := make([]json.RawMessage, len(events))
	var oldest, newest int64
	if len(events) > 0 {
		// events is always oldest-first regardless of paging direction
		// (EventStore.ListEvents's contract), so the cursors are just
		// the endpoints — no need to scan for min/max.
		oldest = events[0].Sequence
		newest = events[len(events)-1].Sequence
	}
	for i, e := range events {
		raw[i] = e.Raw
	}

	msg, err := newMessage(campaignID, protocol.MessageTypeLogHistoryResponse, protocol.HistoryResponsePayload{
		Events:             raw,
		NextBeforeSequence: oldest,
		NextAfterSequence:  newest,
		HasMore:            hasMore,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing log.history_response: %w", err)
	}
	return nil
}

// sendError sends a system.error message to conn — the sender only, not
// a broadcast — explaining why their message was rejected, then returns
// nil so the read loop continues. It only returns a non-nil error if
// writing the rejection itself fails, since that indicates a real
// transport problem rather than a client mistake.
func (s *Server) sendError(ctx context.Context, conn *websocket.Conn, campaignID, inReplyTo string, cause error) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemError, protocol.SystemErrorPayload{
		Code:               "message_rejected",
		Message:            cause.Error(),
		InReplyToMessageID: inReplyTo,
	})
	if err != nil {
		return err
	}
	recordEvent(ctx, s, msg)
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing rejection: %w", err)
	}
	return nil
}

// rejectHandshake sends a system.error message explaining why the
// handshake failed, then closes the connection with a policy-violation
// status. It returns cause so the caller's error path reports the same
// failure it just told the client about.
func (s *Server) rejectHandshake(ctx context.Context, conn *websocket.Conn, inReplyTo, campaignID string, cause error) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeSystemError, protocol.SystemErrorPayload{
		Code:               "handshake_rejected",
		Message:            cause.Error(),
		InReplyToMessageID: inReplyTo,
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	recordEvent(ctx, s, msg)
	// Best-effort: the connection is closing regardless of whether the
	// client receives this, so a write failure here doesn't change the
	// outcome — it only means the client won't know why.
	if werr := wsjson.Write(ctx, conn, msg); werr != nil {
		s.logger.Warn("failed to send handshake rejection", "error", werr)
	}
	conn.Close(websocket.StatusPolicyViolation, "handshake rejected")
	return cause
}

// recordEvent best-effort persists msg to s's event log: a failure here
// is logged, not returned, because losing an audit-log entry shouldn't
// interrupt the live connection that's the actual point of Server. This
// is deliberately different from how design doc §10 describes
// authoritative session/character state, which does need durability
// guarantees — but that state doesn't exist yet (only the handshake
// does), so today this records handshake traffic as an audit trail, not
// authoritative game state. It is a free function taking *Server rather
// than a Server method for the same reason newMessage is: Go methods
// cannot themselves be generic.
func recordEvent[T any](ctx context.Context, s *Server, msg protocol.Message[T]) {
	if s.events == nil {
		return
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		s.logger.Warn("failed to marshal event for persistence", "error", err, "message_id", msg.MessageID)
		return
	}

	err = s.events.AppendEvent(ctx, store.Event{
		CampaignID:  msg.CampaignID,
		MessageID:   msg.MessageID,
		MessageType: string(msg.Type),
		SenderID:    msg.SenderID,
		OccurredAt:  msg.Timestamp,
		Raw:         raw,
	})
	if err != nil && !errors.Is(err, store.ErrDuplicateMessage) {
		s.logger.Warn("failed to persist event", "error", err, "message_id", msg.MessageID)
	}
}

// broadcastMessage marshals msg and delivers it to every client currently
// registered under msg.CampaignID via s.hub — the shared marshal-then-
// broadcast step every Master-originated broadcast (safety.flag_broadcast,
// narrative.player_bubble, roll.request/roll.result) needs. Like
// recordEvent and newMessage, it is a free function rather than a Server
// method because Go methods cannot themselves be generic.
func broadcastMessage[T any](s *Server, msg protocol.Message[T]) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", msg.Type, err)
	}
	s.hub.Broadcast(msg.CampaignID, payload)
	return nil
}

// newMessage builds a Message[T] envelope for a Master-originated
// message: current protocol version, a fresh message_id, current
// timestamp, and Master's sender_id. It is a free function rather than a
// Server method because Go methods cannot themselves be generic; callers
// still read naturally as newMessage(campaignID, type, payload).
func newMessage[T any](campaignID string, msgType protocol.MessageType, payload T) (protocol.Message[T], error) {
	id, err := newMessageID()
	if err != nil {
		return protocol.Message[T]{}, err
	}
	return protocol.Message[T]{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       id,
			Timestamp:       time.Now().UTC(),
			SenderID:        masterSenderID,
			CampaignID:      campaignID,
			Type:            msgType,
		},
		Payload: payload,
	}, nil
}
