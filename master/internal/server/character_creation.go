// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// The four real choices Master's own top-level character-creation
// prompt offers (design doc §9.4). Everything after this first answer
// either stays Master-owned (import, pregen) or hands off to the
// System Engine's own question sequence (quick_roll, detailed_roll) —
// see creationStage.
const (
	creationChoiceImport       = "import"
	creationChoiceQuickRoll    = "quick_roll"
	creationChoiceDetailedRoll = "detailed_roll"
	creationChoicePregen       = "pregen"
)

// creationStage records what kind of answer an in-progress creation
// session is currently waiting on, so handleCreationAnswer knows how to
// interpret the next character.creation_answer it receives — the same
// session_id is reused end to end (Master's own top-level prompt, then
// whichever sub-flow the player picked), so the stage is what actually
// distinguishes "you're answering the top-level choice" from "you're
// pasting character JSON" from "you're answering the engine's own
// question sequence".
type creationStage int

const (
	creationStageTopLevel creationStage = iota
	creationStageImport
	creationStagePregen
	creationStageEngine
)

// creationSession is Master's own record of one in-progress character-
// creation conversation — see the Server.creationSessions doc comment
// for why this is in-memory only.
type creationSession struct {
	senderID string
	stage    creationStage
}

func (s *Server) getCreationSession(sessionID string) (creationSession, bool) {
	s.creationSessionsMu.Lock()
	defer s.creationSessionsMu.Unlock()
	sess, ok := s.creationSessions[sessionID]
	return sess, ok
}

func (s *Server) setCreationSession(sessionID string, sess creationSession) {
	s.creationSessionsMu.Lock()
	defer s.creationSessionsMu.Unlock()
	s.creationSessions[sessionID] = sess
}

func (s *Server) deleteCreationSession(sessionID string) {
	s.creationSessionsMu.Lock()
	defer s.creationSessionsMu.Unlock()
	delete(s.creationSessions, sessionID)
}

// handleCreationStart implements character.creation_start (design doc
// §9.4): the very first message a player sends after joining, in place
// of the old auto-generated stopgap character. Always succeeds with
// Master's own fixed top-level prompt — nothing here depends on the
// System Engine or any other optional dependency, since choosing
// *which* path to take shouldn't itself require one.
func (s *Server) handleCreationStart(ctx context.Context, conn *websocket.Conn, campaignID, senderID string) error {
	sessionID, err := newRandomID()
	if err != nil {
		return err
	}
	s.setCreationSession(sessionID, creationSession{senderID: senderID, stage: creationStageTopLevel})
	return s.sendCreationPrompt(ctx, conn, campaignID, sessionID,
		"How would you like to create your character? Import an existing character, quick roll a new one (you pick race/class/gender, the rest is rolled), detailed roll a new one (you make every choice), or pick a pregenerated character your Host has offered.",
		[]string{creationChoiceImport, creationChoiceQuickRoll, creationChoiceDetailedRoll, creationChoicePregen},
	)
}

// handleCreationAnswer implements character.creation_answer: routes the
// player's answer by whatever stage their session is currently in.
// Rejects outright (system.error) when sessionID is unknown/expired or
// doesn't belong to senderID — Master's own record of who started which
// session, checked before this answer ever reaches anything else,
// including a System Engine session of the same ID.
func (s *Server) handleCreationAnswer(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, req protocol.CharacterCreationAnswerMessage) error {
	sess, ok := s.getCreationSession(req.Payload.SessionID)
	if !ok || sess.senderID != senderID {
		return s.sendError(ctx, conn, campaignID, req.MessageID, errors.New("unknown or expired character-creation session"))
	}

	switch sess.stage {
	case creationStageTopLevel:
		return s.handleCreationTopLevelAnswer(ctx, conn, campaignID, senderID, req.Payload.SessionID, req.Payload.Answer)
	case creationStageImport:
		s.deleteCreationSession(req.Payload.SessionID)
		return s.importCharacter(ctx, conn, campaignID, senderID, protocol.CharacterUploadMessage{
			Envelope: protocol.Envelope{
				ProtocolVersion: protocol.CurrentProtocolVersion,
				MessageID:       req.MessageID,
				Timestamp:       time.Now().UTC(),
				SenderID:        senderID,
				CampaignID:      campaignID,
				Type:            protocol.MessageTypeCharacterUpload,
			},
			Payload: protocol.CharacterUploadPayload{CharacterJSON: req.Payload.Answer},
		})
	case creationStagePregen:
		return s.handleCreationPregenAnswer(ctx, conn, campaignID, senderID, req.Payload.SessionID, req.Payload.Answer)
	case creationStageEngine:
		return s.handleCreationEngineAnswer(ctx, conn, campaignID, senderID, req.Payload.SessionID, req.Payload.Answer)
	default:
		return s.sendError(ctx, conn, campaignID, req.MessageID, fmt.Errorf("internal error: unrecognized creation stage %v", sess.stage))
	}
}

// handleCreationTopLevelAnswer handles the player's answer to Master's
// own fixed top-level prompt.
func (s *Server) handleCreationTopLevelAnswer(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID, answer string) error {
	switch answer {
	case creationChoiceImport:
		s.setCreationSession(sessionID, creationSession{senderID: senderID, stage: creationStageImport})
		return s.sendCreationPrompt(ctx, conn, campaignID, sessionID, "Paste your character's JSON.", nil)

	case creationChoicePregen:
		return s.startCreationPregenChoice(ctx, conn, campaignID, senderID, sessionID)

	case creationChoiceQuickRoll, creationChoiceDetailedRoll:
		return s.startCreationRoll(ctx, conn, campaignID, senderID, sessionID, answer == creationChoiceDetailedRoll)

	default:
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("%q is not import, quick_roll, detailed_roll, or pregen", answer))
	}
}

// startCreationPregenChoice lists campaignID's real pregens (design doc
// §9.4's "pick one the Host offers") as a creation prompt whose choices
// are each pregen's own ID — an ID, not its display Name, is what the
// player's answer echoes back, so two pregens sharing a display name
// can never be ambiguous; the prompt text itself still shows the
// human-readable name and description for each.
func (s *Server) startCreationPregenChoice(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID string) error {
	if s.pregens == nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", errors.New("no pregenerated characters are configured for this campaign"))
	}
	pregens, err := s.pregens.ListPregens(ctx, campaignID)
	if err != nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("listing pregens: %w", err))
	}
	if len(pregens) == 0 {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", errors.New("no pregenerated characters are configured for this campaign"))
	}

	var text strings.Builder
	text.WriteString("Choose a pregenerated character:\n")
	choices := make([]string, len(pregens))
	for i, p := range pregens {
		choices[i] = p.ID
		fmt.Fprintf(&text, "- %s: %s — %s\n", p.ID, p.Name, p.Description)
	}
	s.setCreationSession(sessionID, creationSession{senderID: senderID, stage: creationStagePregen})
	return s.sendCreationPrompt(ctx, conn, campaignID, sessionID, text.String(), choices)
}

// handleCreationPregenAnswer claims pregenID: copies its CharacterData
// into a brand-new store.Character owned by senderID — never mutates
// the template row, so the same pregen can be claimed by more than one
// player independently (design doc §9.4).
func (s *Server) handleCreationPregenAnswer(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID, pregenID string) error {
	s.deleteCreationSession(sessionID)
	if s.pregens == nil || s.characters == nil {
		return s.sendError(ctx, conn, campaignID, "", errors.New("pregens are not configured on this campaign"))
	}
	pregen, err := s.pregens.GetPregen(ctx, pregenID)
	if err != nil || pregen.CampaignID != campaignID {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("%q is not a real pregen for this campaign", pregenID))
	}

	characterID, err := newCharacterID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, store.Character{
		ID:            characterID,
		CampaignID:    campaignID,
		OwnerID:       senderID,
		SchemaVersion: pregen.SchemaVersion,
		Status:        store.CharacterStatusApproved,
		CharacterData: pregen.CharacterData,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("saving claimed pregen: %w", err))
	}
	return s.sendCreationComplete(ctx, conn, campaignID, characterID)
}

// startCreationRoll begins an engine-driven roll (design doc §9.4's
// quick/detailed options) — the System Engine owns the entire question
// sequence from here; Master only relays. characterName is the
// player's own already-typed display name (senderID doubles as it,
// same as everywhere else in this codebase — see web/app.js's
// onJoinClick) — never asked as a separate prompt.
func (s *Server) startCreationRoll(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID string, detailed bool) error {
	if s.systemEngine == nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", errors.New("character rolling unavailable: no system engine configured"))
	}
	mode := systemenginepb.CharacterCreationMode_CHARACTER_CREATION_MODE_QUICK
	if detailed {
		mode = systemenginepb.CharacterCreationMode_CHARACTER_CREATION_MODE_DETAILED
	}
	resp, err := s.systemEngine.StartCharacterCreation(ctx, &systemenginepb.StartCharacterCreationRequest{
		SessionId:     sessionID,
		CampaignId:    campaignID,
		Mode:          mode,
		CharacterName: senderID,
	})
	if err != nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("calling system engine StartCharacterCreation: %w", err))
	}
	return s.handleCreationEngineResponse(ctx, conn, campaignID, senderID, sessionID, resp)
}

// handleCreationEngineAnswer forwards one answer to the System Engine's
// own in-progress session of the same ID.
func (s *Server) handleCreationEngineAnswer(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID, answer string) error {
	if s.systemEngine == nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", errors.New("character rolling unavailable: no system engine configured"))
	}
	resp, err := s.systemEngine.AnswerCharacterCreationPrompt(ctx, &systemenginepb.AnswerCharacterCreationPromptRequest{
		SessionId: sessionID,
		Answer:    answer,
	})
	if err != nil {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("calling system engine AnswerCharacterCreationPrompt: %w", err))
	}
	return s.handleCreationEngineResponse(ctx, conn, campaignID, senderID, sessionID, resp)
}

// handleCreationEngineResponse is the shared tail of
// startCreationRoll/handleCreationEngineAnswer: either relay the
// engine's next prompt, or (once done) save the finished character.
func (s *Server) handleCreationEngineResponse(ctx context.Context, conn *websocket.Conn, campaignID, senderID, sessionID string, resp *systemenginepb.CharacterCreationPromptResponse) error {
	if !resp.Success {
		s.deleteCreationSession(sessionID)
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("character creation failed: %s", resp.Error))
	}
	if resp.Done {
		s.deleteCreationSession(sessionID)
		return s.finishCreationRoll(ctx, conn, campaignID, senderID, resp.Actor)
	}
	s.setCreationSession(sessionID, creationSession{senderID: senderID, stage: creationStageEngine})
	return s.sendCreationPrompt(ctx, conn, campaignID, sessionID, resp.PromptText, resp.Choices)
}

// finishCreationRoll persists a System-Engine-generated character —
// Status: CharacterStatusApproved, not PendingReview: unlike an
// arbitrary pasted-JSON import, this data was never anything but
// mechanically generated by the engine itself, so there's nothing for
// design doc §9.4's human review panel to check that the engine hasn't
// already guaranteed.
func (s *Server) finishCreationRoll(ctx context.Context, conn *websocket.Conn, campaignID, senderID string, actor *systemenginepb.Actor) error {
	if actor == nil {
		return s.sendError(ctx, conn, campaignID, "", errors.New("system engine reported a finished character with no actor data"))
	}
	characterData, err := protojson.Marshal(actor.CharacterData)
	if err != nil {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("marshaling generated character data: %w", err))
	}
	characterID, err := newCharacterID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, store.Character{
		ID:            characterID,
		CampaignID:    campaignID,
		OwnerID:       senderID,
		SchemaVersion: actor.SchemaVersion,
		Status:        store.CharacterStatusApproved,
		CharacterData: characterData,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return s.sendError(ctx, conn, campaignID, "", fmt.Errorf("saving rolled character: %w", err))
	}
	return s.sendCreationComplete(ctx, conn, campaignID, characterID)
}

// sendCreationComplete sends the character.validation_result that
// completes a creation flow (rolled or claimed-pregen) — deliberately
// the same message type character.upload's own successful import
// already answers with, not character.state: the client's existing
// completion handling (onCharacterValidationResult — set state.
// rollCharacterId, fetch the schema, activate the dice tray) already
// triggers off character.validation_result specifically, and
// character.state is separately, ambiguously used for ordinary live
// character.get replies during play — reusing it here would leave the
// client unable to tell "your new character is ready" apart from "here
// is a routine state refresh." No warnings: unlike an arbitrary
// pasted-JSON import, a rolled or pregen-claimed character was never
// anything but mechanically valid to begin with.
func (s *Server) sendCreationComplete(ctx context.Context, conn *websocket.Conn, campaignID, characterID string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterValidationResult, protocol.CharacterValidationResultPayload{
		CharacterID: characterID,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.validation_result: %w", err)
	}
	return nil
}

// sendCreationPrompt sends one character.creation_prompt directly on
// conn — a plain reply on the requesting player's own connection, the
// same pattern character.get/character.upload's own replies already
// use, which is exactly why this needs no new privacy mechanism: nobody
// but this connection is reading it.
func (s *Server) sendCreationPrompt(ctx context.Context, conn *websocket.Conn, campaignID, sessionID, promptText string, choices []string) error {
	msg, err := newMessage(campaignID, protocol.MessageTypeCharacterCreationPrompt, protocol.CharacterCreationPromptPayload{
		SessionID:  sessionID,
		PromptText: promptText,
		Choices:    choices,
	})
	if err != nil {
		return err
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		return fmt.Errorf("writing character.creation_prompt: %w", err)
	}
	return nil
}
