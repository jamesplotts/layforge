// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

// MessageType discriminates which concrete message an Envelope carries,
// mirroring the "type" discriminator in protocol/asyncapi.yaml's Envelope
// schema (e.g. "system.connect", "narrative.dm_prose").
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
	MessageTypeRollRequest               MessageType = "roll.request"
	MessageTypeRollResult                MessageType = "roll.result"
	MessageTypeCharacterSchemaRequest    MessageType = "character.schema_request"
	MessageTypeCharacterSchemaResponse   MessageType = "character.schema_response"
	MessageTypeCharacterGet              MessageType = "character.get"
	MessageTypeCharacterState            MessageType = "character.state"
	MessageTypeCharacterApplyEffect      MessageType = "character.apply_effect"
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
		MessageTypeRollCheckRequest, MessageTypeRollRequest, MessageTypeRollResult,
		MessageTypeCharacterSchemaRequest, MessageTypeCharacterSchemaResponse,
		MessageTypeCharacterGet, MessageTypeCharacterState,
		MessageTypeCharacterApplyEffect:
		return true
	default:
		return false
	}
}
