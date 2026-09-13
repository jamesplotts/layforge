// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

// MessageType discriminates which concrete message an Envelope carries,
// mirroring the "type" discriminator in protocol/asyncapi.yaml's Envelope
// schema (e.g. "system.connect", "client.display").
//
// The zero value, MessageTypeUnspecified, is never valid on the wire —
// see IsValid. This is the Go translation of the Unspecified/LastValue
// enum-sentinel pattern from design doc §12: Go has no enum range to
// bound with a LastValue, so IsValid's switch is the range check instead
// (see CLAUDE.md).
type MessageType string

// Recognized message types. Only the ones Master currently implements
// are listed here — extend this as protocol/asyncapi.yaml grows and
// Master implements more of it; an unrecognized type on the wire is
// rejected by IsValid, not silently accepted.
const (
	MessageTypeUnspecified               MessageType = ""
	MessageTypeSystemConnect             MessageType = "system.connect"
	MessageTypeSystemSessionState        MessageType = "system.session_state"
	MessageTypeSystemError               MessageType = "system.error"
	MessageTypeSafetyFlag                MessageType = "safety.flag"
	MessageTypeSafetyFlagBroadcast       MessageType = "safety.flag_broadcast"
	MessageTypeLogHistoryRequest         MessageType = "log.history_request"
	MessageTypeLogHistoryResponse        MessageType = "log.history_response"
	MessageTypeNarrativePlayerInput      MessageType = "narrative.player_input"
	MessageTypeNarrativePlayerBubble     MessageType = "narrative.player_bubble"
	MessageTypeCharacterUpload           MessageType = "character.upload"
	MessageTypeCharacterValidationResult MessageType = "character.validation_result"
	MessageTypeRollCheckRequest          MessageType = "roll.check_request"
	MessageTypeCharacterSchemaRequest    MessageType = "character.schema_request"
	MessageTypeCharacterSchemaResponse   MessageType = "character.schema_response"
	MessageTypeCharacterGet              MessageType = "character.get"
	MessageTypeCharacterState            MessageType = "character.state"
	MessageTypeCharacterApplyEffect      MessageType = "character.apply_effect"
	MessageTypeToolResult                MessageType = "tool.result"
	MessageTypeTurnState                 MessageType = "turn.state"
	MessageTypeMapTokenState             MessageType = "map.token_state"
	MessageTypeMapTokenMoveRequest       MessageType = "map.token_move_request"
	MessageTypeVehicleImport             MessageType = "vehicle.import"
	MessageTypeVehicleImported           MessageType = "vehicle.imported"
	MessageTypeAudioChunk                MessageType = "audio.chunk"
	MessageTypeAudioTranscription        MessageType = "audio.transcription"
	MessageTypeCharacterCreationStart    MessageType = "character.creation_start"
	MessageTypeCharacterReviewResult     MessageType = "character.review_result"

	// The client.* family (design doc §4) — the small, reusable set of
	// bubble interactions Master uses to talk to a player: a one-way text
	// bubble (client.display), a typed-answer prompt (client.query /
	// client.query_response), a pick-one prompt (client.choice /
	// client.choice_response), an image bubble (client.image), and the
	// interactive dice-roll exchange (client.roll and friends — see
	// messages.go and internal/server/client_roll.go for the roller/
	// spectator reveal sequence) — the one and only path a resolved
	// check reaches a client through; roll.check_request's own response
	// no longer goes through a separate roll.request/roll.result pair.
	MessageTypeClientDisplay            MessageType = "client.display"
	MessageTypeClientQuery              MessageType = "client.query"
	MessageTypeClientQueryResponse      MessageType = "client.query_response"
	MessageTypeClientChoice             MessageType = "client.choice"
	MessageTypeClientChoiceResponse     MessageType = "client.choice_response"
	MessageTypeClientImage              MessageType = "client.image"
	MessageTypeClientRoll               MessageType = "client.roll"
	MessageTypeClientRollSpectate       MessageType = "client.roll_spectate"
	MessageTypeClientRollReveal         MessageType = "client.roll_reveal"
	MessageTypeClientRollSpectateReveal MessageType = "client.roll_spectate_reveal"
	MessageTypeClientRollComplete       MessageType = "client.roll_complete"
	// MessageTypeClientAbilityScoreRolls/MessageTypeClientAbilityScoreRollsAck
	// are the 4d6-drop-lowest ability-score reveal exchange (character
	// creation only, design doc §9.4) — a private, single-player
	// conversation with no spectator to hide a result from, unlike
	// client.roll above, so there is no spectate/reveal-relay pair here:
	// Master sends every rolled die up front and the client's reveal is a
	// local animation, only acking once done. See internal/server/
	// character_creation.go.
	MessageTypeClientAbilityScoreRolls    MessageType = "client.ability_score_rolls"
	MessageTypeClientAbilityScoreRollsAck MessageType = "client.ability_score_rolls_ack"
	// MessageTypeTermsAccept is sent by a client once per connection to
	// accept the current terms.Version (see internal/terms and
	// internal/server's dispatch gate) — required before any other
	// message type is processed for that connection.
	MessageTypeTermsAccept MessageType = "terms.accept"
)

// IsValid reports whether t is one of the message types this build of
// Master understands. It deliberately returns false for
// MessageTypeUnspecified.
func (t MessageType) IsValid() bool {
	switch t {
	case MessageTypeSystemConnect, MessageTypeSystemSessionState, MessageTypeSystemError,
		MessageTypeSafetyFlag, MessageTypeSafetyFlagBroadcast,
		MessageTypeLogHistoryRequest, MessageTypeLogHistoryResponse,
		MessageTypeNarrativePlayerInput, MessageTypeNarrativePlayerBubble,
		MessageTypeCharacterUpload, MessageTypeCharacterValidationResult,
		MessageTypeRollCheckRequest,
		MessageTypeCharacterSchemaRequest, MessageTypeCharacterSchemaResponse,
		MessageTypeCharacterGet, MessageTypeCharacterState,
		MessageTypeCharacterApplyEffect,
		MessageTypeToolResult, MessageTypeTurnState,
		MessageTypeMapTokenState, MessageTypeMapTokenMoveRequest,
		MessageTypeVehicleImport, MessageTypeVehicleImported,
		MessageTypeAudioChunk, MessageTypeAudioTranscription,
		MessageTypeCharacterCreationStart,
		MessageTypeCharacterReviewResult, MessageTypeTermsAccept,
		MessageTypeClientDisplay, MessageTypeClientQuery, MessageTypeClientQueryResponse,
		MessageTypeClientChoice, MessageTypeClientChoiceResponse, MessageTypeClientImage,
		MessageTypeClientRoll, MessageTypeClientRollSpectate, MessageTypeClientRollReveal,
		MessageTypeClientRollSpectateReveal, MessageTypeClientRollComplete,
		MessageTypeClientAbilityScoreRolls, MessageTypeClientAbilityScoreRollsAck:
		return true
	default:
		return false
	}
}
