// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

import "encoding/json"

// Message pairs an Envelope with its typed Payload, matching the flat
// JSON shape protocol/asyncapi.yaml describes via allOf: envelope fields
// and "payload" side by side in one object. Envelope's exported fields
// are promoted to the outer JSON object by Go's embedding rules, so a
// Message[T] round-trips to exactly {protocol_version, message_id,
// timestamp, sender_id, campaign_id, type, payload: {...}}.
type Message[T any] struct {
	Envelope
	Payload T `json:"payload"`
}

// SystemConnectPayload is the payload of a system.connect message — the
// first message a client sends after opening the WebSocket, identifying
// itself and negotiating protocol_version via the envelope. See
// protocol/asyncapi.yaml components.messages.SystemConnect.
type SystemConnectPayload struct {
	// ClientKind identifies the client implementation, e.g.
	// "player_web_v1", or a third-party viewport's own identifier
	// (design doc §4).
	ClientKind string `json:"client_kind"`
	AuthToken  string `json:"auth_token"`
}

// SystemConnectMessage is a system.connect Message.
type SystemConnectMessage = Message[SystemConnectPayload]

// SystemSessionStatePayload is the payload of a system.session_state
// message: connection/session lifecycle notifications. See protocol/
// asyncapi.yaml components.messages.SystemSessionState.
type SystemSessionStatePayload struct {
	State SessionState `json:"state"`
	// CharacterID is set when State concerns a specific character
	// joining/leaving, and omitted for session-wide states like paused/
	// resumed.
	CharacterID string `json:"character_id,omitempty"`
}

// SystemSessionStateMessage is a system.session_state Message.
type SystemSessionStateMessage = Message[SystemSessionStatePayload]

// SystemErrorPayload is the payload of a system.error message: a
// server-side error not tied to a specific tool call, e.g. a malformed
// message or a rejected handshake. See protocol/asyncapi.yaml
// components.messages.SystemError.
type SystemErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// InReplyToMessageID is the message_id of the client message that
	// caused this error, when there is one to point at.
	InReplyToMessageID string `json:"in_reply_to_message_id,omitempty"`
}

// SystemErrorMessage is a system.error Message.
type SystemErrorMessage = Message[SystemErrorPayload]

// SafetyFlagPayload is the payload of a safety.flag message: any client
// may send this at any time to invoke the X-card/veil safety tool. See
// design doc §9.2 and protocol/asyncapi.yaml components.messages.SafetyFlag.
type SafetyFlagPayload struct {
	// Topic is an optional topic tag; omitted for a bare X-card.
	Topic string `json:"topic,omitempty"`
}

// SafetyFlagMessage is a safety.flag Message.
type SafetyFlagMessage = Message[SafetyFlagPayload]

// SafetyFlagBroadcastPayload is the payload of a safety.flag_broadcast
// message: Master's rebroadcast of a received safety.flag to every
// client in the campaign, deliberately not naming who sent it. See
// design doc §9.2 and protocol/asyncapi.yaml
// components.messages.SafetyFlagBroadcast.
type SafetyFlagBroadcastPayload struct {
	Topic string `json:"topic,omitempty"`
}

// SafetyFlagBroadcastMessage is a safety.flag_broadcast Message.
type SafetyFlagBroadcastMessage = Message[SafetyFlagBroadcastPayload]

// HistoryRequestPayload is the payload of a log.history_request message:
// a client asking for a page of the durable campaign event log (design
// doc §10) for chat-log/history review (§11). Set at most one of
// AfterSequence / BeforeSequence — they're opposite paging directions;
// setting both gets a system.error. See protocol/asyncapi.yaml
// components.messages.LogHistoryRequest.
type HistoryRequestPayload struct {
	// AfterSequence, if non-zero, returns events after this store-
	// assigned sequence number — not a message_id or timestamp; see
	// design doc §10 on why sequence, not client-supplied fields, is the
	// pagination cursor. Typically a previous response's
	// NextAfterSequence.
	AfterSequence int64 `json:"after_sequence,omitempty"`
	// BeforeSequence, if non-zero, returns events before this sequence
	// number — "load earlier," typically a previous response's
	// NextBeforeSequence.
	BeforeSequence int64 `json:"before_sequence,omitempty"`
	// Limit caps how many events come back; zero (or an oversized value)
	// falls back to Master's own default/cap.
	Limit int `json:"limit,omitempty"`
}

// HistoryRequestMessage is a log.history_request Message.
type HistoryRequestMessage = Message[HistoryRequestPayload]

// HistoryResponsePayload is the payload of a log.history_response
// message: a page of previously recorded messages, each returned exactly
// as it was originally sent — not re-wrapped in any further envelope, so
// a client renders history the same way it renders anything live.
// Events is always oldest-first, regardless of which direction the
// request paged, or of neither being set — which returns the most
// recent messages (the natural first page for a chat-style scrollback),
// not the campaign's very first message. See protocol/asyncapi.yaml
// components.messages.LogHistoryResponse.
type HistoryResponsePayload struct {
	Events []json.RawMessage `json:"events"`
	// NextBeforeSequence: pass as BeforeSequence on a follow-up request
	// to continue paging toward older history ("load earlier").
	// Omitted when Events is empty.
	NextBeforeSequence int64 `json:"next_before_sequence,omitempty"`
	// NextAfterSequence: pass as AfterSequence on a follow-up request to
	// continue paging toward newer history. Omitted when Events is
	// empty.
	NextAfterSequence int64 `json:"next_after_sequence,omitempty"`
	// HasMore indicates whether more events exist in the direction this
	// request paged: newer if the request set AfterSequence, older
	// otherwise (including the default/tail case).
	HasMore bool `json:"has_more"`
}

// HistoryResponseMessage is a log.history_response Message.
type HistoryResponseMessage = Message[HistoryResponsePayload]

// NarrativeInputSource records how the player input a
// NarrativePlayerInputPayload's Text — typed, or finalized from voice
// transcription (design doc §4, §7). The zero value,
// NarrativeInputSourceUnspecified, is never valid on the wire.
type NarrativeInputSource string

const (
	NarrativeInputSourceUnspecified NarrativeInputSource = ""
	NarrativeInputSourceTyped       NarrativeInputSource = "typed"
	NarrativeInputSourceVoice       NarrativeInputSource = "voice"
)

// IsValid reports whether s is a recognized input source. It
// deliberately returns false for NarrativeInputSourceUnspecified.
func (s NarrativeInputSource) IsValid() bool {
	switch s {
	case NarrativeInputSourceTyped, NarrativeInputSourceVoice:
		return true
	default:
		return false
	}
}

// NarrativePlayerInputPayload is the payload of a narrative.player_input
// message: a player's raw typed or voice-transcribed action/dialogue,
// which Master's narrative-transform pipeline (design doc §7) renders
// into a NarrativePlayerBubblePayload before broadcast — never shown to
// other players verbatim. See protocol/asyncapi.yaml
// components.messages.NarrativePlayerInput.
type NarrativePlayerInputPayload struct {
	CharacterID string               `json:"character_id"`
	Text        string               `json:"text"`
	Source      NarrativeInputSource `json:"source"`
}

// NarrativePlayerInputMessage is a narrative.player_input Message.
type NarrativePlayerInputMessage = Message[NarrativePlayerInputPayload]

// NarrativePlayerBubblePayload is the payload of a narrative.player_bubble
// message: the rendered, third-person, DM-voiced prose for a player's own
// stated action/dialogue (design doc §7's fast pass). See protocol/
// asyncapi.yaml components.messages.NarrativePlayerBubble.
type NarrativePlayerBubblePayload struct {
	CharacterID string `json:"character_id"`
	Text        string `json:"text"`
	// Visibility is nil (omitted) until knowledge scoping (design doc
	// §9.7) is actually enforced — see VisibilityScope's doc comment.
	Visibility *VisibilityScope `json:"visibility,omitempty"`
	// Editable is true if the originating player may still edit/
	// regenerate this bubble (design doc §7) — not enforced anywhere yet;
	// Master doesn't implement narrative.regenerate_request.
	Editable bool `json:"editable,omitempty"`
}

// NarrativePlayerBubbleMessage is a narrative.player_bubble Message.
type NarrativePlayerBubbleMessage = Message[NarrativePlayerBubblePayload]

// CharacterUploadPayload is the payload of a character.upload message: a
// player uploading a character whose JSON conforms to the active system
// engine's schema (design doc §6.1, §9.4). See protocol/asyncapi.yaml
// components.messages.CharacterUpload.
type CharacterUploadPayload struct {
	// CharacterJSON is the character's serialized data, in the shape the
	// system engine's GetCharacterSchema publishes — opaque to Master
	// beyond forwarding it to the system engine's FromJson.
	CharacterJSON string `json:"character_json"`
	// SchemaVersion is the schema_version CharacterJSON was produced
	// against (design doc §6.1).
	SchemaVersion string `json:"schema_version"`
}

// CharacterUploadMessage is a character.upload Message.
type CharacterUploadMessage = Message[CharacterUploadPayload]

// CharacterValidationWarning mirrors the System Engine gRPC contract's
// ValidationWarning message field-for-field (protocol/system_engine.proto)
// — Master forwards the engine's own mechanical-validation findings to the
// client rather than reinterpreting them.
type CharacterValidationWarning struct {
	FieldPath string `json:"field_path"`
	Message   string `json:"message"`
	// Severity is "error" or "warning" — "error" means the character
	// should not be importable as-is (design doc §9.4).
	Severity string `json:"severity"`
}

// CharacterValidationResultPayload is the payload of a
// character.validation_result message: the system engine's
// validate_character()/from_json() mechanical findings for an uploaded
// character (design doc §9.4). See protocol/asyncapi.yaml
// components.messages.CharacterValidationResult.
//
// NarrativeFlags (the DM's freeform lore-consistency pass) is always
// omitted today — Master has no narrative-flagging pass implemented yet,
// only the system engine's mechanical validation.
type CharacterValidationResultPayload struct {
	// CharacterID is Master's own identifier for the uploaded character
	// (store.Character.ID), not anything the system engine assigns.
	CharacterID string                       `json:"character_id"`
	Warnings    []CharacterValidationWarning `json:"warnings"`
	// NarrativeFlags: see the type doc comment — always omitted today.
	NarrativeFlags []string `json:"narrative_flags,omitempty"`
}

// CharacterValidationResultMessage is a character.validation_result
// Message.
type CharacterValidationResultMessage = Message[CharacterValidationResultPayload]

// RollCheckRequestPayload is the payload of a roll.check_request message:
// a player asking Master to resolve a mechanical check for a character
// they own (design doc §9.4's OwnerID gates this — see
// server.importCharacter/store.Character.OwnerID). CheckType/Ability/Skill
// are engine-agnostic strings, not an enum, mirroring the System Engine
// gRPC contract's own engine-defined ResolveCheck params (design doc
// §6.1) — this message isn't d20-specific. See protocol/asyncapi.yaml
// components.messages.RollCheckRequest.
type RollCheckRequestPayload struct {
	CharacterID string `json:"character_id"`
	// CheckType is engine-defined, e.g. "ability_check", "saving_throw",
	// "death_save" — whatever the active system engine's ResolveCheck
	// documents it accepts.
	CheckType string `json:"check_type"`
	// Ability is required for CheckType values that need one (e.g.
	// "ability_check", "saving_throw").
	Ability string `json:"ability,omitempty"`
	// Skill is optional, for CheckType "ability_check".
	Skill string `json:"skill,omitempty"`
}

// RollCheckRequestMessage is a roll.check_request Message.
type RollCheckRequestMessage = Message[RollCheckRequestPayload]

// RollDie describes one die in a RollSpec: sides and how many of that die
// are rolled — not an individual result, see DieRoll for that. See
// protocol/asyncapi.yaml components.schemas.RollSpec.
type RollDie struct {
	Sides int `json:"sides"`
	Count int `json:"count"`
}

// RollSpec describes the dice about to be rolled, sourced from the system
// engine's actual resolved Outcome (design doc §4) rather than assumed —
// Master never hardcodes "d20" here; see server's roll.check_request
// dispatch for how RollSpec.Dice is derived from a real ResolveCheck
// response before RollRequestPayload is sent. See protocol/asyncapi.yaml
// components.schemas.RollSpec.
type RollSpec struct {
	Dice []RollDie `json:"dice"`
	// SuccessThreshold: for pool-based systems (count successes at or
	// above this value); omitted for simple total-vs-DC systems.
	SuccessThreshold *int `json:"success_threshold,omitempty"`
	// BotchRule is an engine-defined description of botch/fumble
	// handling, if any.
	BotchRule string `json:"botch_rule,omitempty"`
}

// RollRequestPayload is the payload of a roll.request message: Master
// informing every client in the campaign that a roll is about to happen,
// so a dice-tray UI can pre-stage its animation before the actual result
// is known (design doc §3.1, §4). See protocol/asyncapi.yaml
// components.messages.RollRequest.
type RollRequestPayload struct {
	CharacterID string   `json:"character_id"`
	RollSpec    RollSpec `json:"roll_spec"`
}

// RollRequestMessage is a roll.request Message.
type RollRequestMessage = Message[RollRequestPayload]

// DieRoll is one resolved die's face value — mirrors the System Engine
// gRPC contract's DieRoll message field-for-field
// (protocol/system_engine.proto), since Master forwards the engine's own
// resolved dice rather than reinterpreting them.
type DieRoll struct {
	Sides  int    `json:"sides"`
	Result int    `json:"result"`
	Label  string `json:"label,omitempty"`
}

// RollResultPayload is the payload of a roll.result message: the
// authoritative, server-computed outcome of a roll.check_request. Clients
// animate their dice tray to land on this outcome; they never determine
// it themselves (design doc §3.1, §4). See protocol/asyncapi.yaml
// components.messages.RollResult.
type RollResultPayload struct {
	CharacterID   string    `json:"character_id"`
	Rolls         []DieRoll `json:"rolls"`
	Total         int       `json:"total"`
	ResultSummary string    `json:"result_summary,omitempty"`
}

// RollResultMessage is a roll.result Message.
type RollResultMessage = Message[RollResultPayload]

// CharacterSchemaRequestPayload is the payload of a
// character.schema_request message: a client asking for the active
// system engine's character schema (design doc §4, §6.1) — schema-wide,
// not per-character; a client typically fetches this once and reuses it
// for every character sheet it renders. See protocol/asyncapi.yaml
// components.messages.CharacterSchemaRequest.
type CharacterSchemaRequestPayload struct{}

// CharacterSchemaRequestMessage is a character.schema_request Message.
type CharacterSchemaRequestMessage = Message[CharacterSchemaRequestPayload]

// CharacterSchemaResponsePayload is the payload of a
// character.schema_response message: the system engine's own
// get_character_schema() output, forwarded unchanged. See protocol/
// asyncapi.yaml components.messages.CharacterSchemaResponse.
type CharacterSchemaResponsePayload struct {
	SchemaVersion string `json:"schema_version"`
	// JSONSchema is JSON Schema draft 2020-12, serialized as a JSON
	// string (kept as a string, not a nested object, so the schema
	// document's own keywords like "$ref" round-trip exactly — matches
	// how the System Engine gRPC contract itself carries it).
	JSONSchema string `json:"json_schema"`
}

// CharacterSchemaResponseMessage is a character.schema_response Message.
type CharacterSchemaResponseMessage = Message[CharacterSchemaResponsePayload]

// CharacterGetPayload is the payload of a character.get message: a
// player asking for a previously-uploaded character's current state.
// Master rejects the request (system.error) if the sender doesn't own
// that character — store.Character.OwnerID, the same gate
// RollCheckRequestPayload's dispatch uses. See protocol/asyncapi.yaml
// components.messages.CharacterGet.
type CharacterGetPayload struct {
	CharacterID string `json:"character_id"`
}

// CharacterGetMessage is a character.get Message.
type CharacterGetMessage = Message[CharacterGetPayload]

// CharacterStatePayload is the payload of a character.state message,
// answering character.get. CharacterData conforms to whatever schema
// character.schema_response's JSONSchema currently describes — opaque to
// Master beyond that, same as everywhere else character_data appears
// (design doc §6.1). Status is the system engine's own
// get_character_status() (design doc §9.3), not something Master infers.
// See protocol/asyncapi.yaml components.messages.CharacterState.
type CharacterStatePayload struct {
	CharacterID   string          `json:"character_id"`
	SchemaVersion string          `json:"schema_version"`
	CharacterData json.RawMessage `json:"character_data"`
	// Status is one of "active", "unconscious", "dying", "dead" — see
	// protocol/system_engine.proto's CharacterStatus enum, mapped to
	// these lowercase strings (design doc §9.3's own vocabulary) by
	// server.characterStatusString.
	Status string `json:"status"`
}

// CharacterStateMessage is a character.state Message.
type CharacterStateMessage = Message[CharacterStatePayload]

// CharacterApplyEffectPayload is the payload of a character.apply_effect
// message: a player applying a mechanical effect to a character they
// own. Effect is opaque, engine-defined JSON (design doc §6.1's
// apply_effect()) — Master forwards it to the system engine unchanged,
// the same way RollCheckRequestPayload's CheckType/Ability/Skill are
// engine-defined strings rather than a closed enum; the concrete effect
// shape (e.g. "damage" with an amount) is knowledge that lives in a
// client's own UI for the active system engine, not in Master. See
// protocol/asyncapi.yaml components.messages.CharacterApplyEffect.
type CharacterApplyEffectPayload struct {
	CharacterID string          `json:"character_id"`
	Effect      json.RawMessage `json:"effect"`
}

// CharacterApplyEffectMessage is a character.apply_effect Message.
type CharacterApplyEffectMessage = Message[CharacterApplyEffectPayload]

// NarrativeDmProsePayload is the payload of a narrative.dm_prose
// message: DM/NPC narration, the slow-pass output of the narrative-
// transform pipeline (design doc §7) — see server's runSlowPass.
// Visibility is nil (omitted) until knowledge scoping (design doc §9.7)
// is actually enforced, same as NarrativePlayerBubblePayload's. See
// protocol/asyncapi.yaml components.messages.NarrativeDmProse.
type NarrativeDmProsePayload struct {
	Text       string           `json:"text"`
	Visibility *VisibilityScope `json:"visibility,omitempty"`
	// InReplyToMessageID is the message_id of the player input this
	// narrates in response to, if any.
	InReplyToMessageID string `json:"in_reply_to_message_id,omitempty"`
}

// NarrativeDmProseMessage is a narrative.dm_prose Message.
type NarrativeDmProseMessage = Message[NarrativeDmProsePayload]

// ToolResultPayload is the payload of a tool.result message: broadcast
// of a completed DM tool-use call, for transparency/logging (design doc
// §8). Governance-gate rejections (design doc §9) would surface here as
// Success=false — no governance-gate engine exists yet beyond
// safety.flag, so today Success=false only ever means the call itself
// failed (bad arguments, an engine error), never a policy rejection. See
// protocol/asyncapi.yaml components.messages.ToolResult.
type ToolResultPayload struct {
	ToolName string `json:"tool_name"`
	// Caller is "dm" or a specific character_id (design doc §8's logging
	// requirement) — always "dm" today, since nothing but the DM model
	// calls these tools yet.
	Caller  string `json:"caller"`
	Success bool   `json:"success"`
	// ReasonCode is set on failure, e.g. "invalid_arguments",
	// "character_not_found", "engine_error".
	ReasonCode string `json:"reason_code,omitempty"`
}

// ToolResultMessage is a tool.result Message.
type ToolResultMessage = Message[ToolResultPayload]

// TurnStatePayload is the payload of a turn.state message: broadcast to
// the whole campaign whenever Master's turn-order state machine changes
// (design doc §3.1, §9.3) — combat starting, advancing to the next
// character's turn, or ending. Master owns this bookkeeping itself,
// independent of the DM model's own judgment about when a turn "feels"
// over; see internal/server/turn_order.go.
type TurnStatePayload struct {
	// Active is false once combat has ended (or before it's ever
	// started) — the rest of the fields are meaningless when false and
	// should be ignored by a client, not treated as "nobody's turn."
	Active bool `json:"active"`
	// Order lists every character_id in initiative order, established
	// once at combat start (highest Dexterity check first) and fixed for
	// the rest of the encounter — reordering mid-combat isn't SRD-typical
	// and isn't implemented.
	Order []string `json:"order,omitempty"`
	// CurrentCharacterID is whose turn it is right now — always a member
	// of Order, and never a character GetCharacterStatus reported dead
	// (design doc §9.3's requirement enforced here, not left to the DM
	// to remember). May be an unconscious/dying character — SRD play
	// still gives them a turn, they roll a death saving throw instead of
	// acting (see internal/server/turn_order.go's startTurnFor).
	CurrentCharacterID string `json:"current_character_id,omitempty"`
	// Round counts full trips through Order, starting at 1 once combat
	// begins.
	Round int `json:"round,omitempty"`
}

// TurnStateMessage is a turn.state Message.
type TurnStateMessage = Message[TurnStatePayload]

// NarrativeSceneImagePayload is the payload of a narrative.scene_image
// message: broadcast to the whole campaign when the DM generates a
// scene illustration (design doc §6.3's generate_scene_image tool, see
// internal/server/dm_tools.go). Master neither authors nor stores the
// image itself — ImageURL points at wherever the configured
// imagegen.Provider actually hosts it (e.g. a self-hosted ComfyUI
// instance's own /view endpoint).
type NarrativeSceneImagePayload struct {
	ImageURL string `json:"image_url"`
	// Prompt is the scene description actually sent to the image
	// generator (including any maturity-tier constraint appended to it,
	// design doc §9.5) — surfaced for transparency, the same reasoning
	// as tool.result logging every DM tool call regardless of outcome.
	Prompt string `json:"prompt"`
	// InReplyToMessageID, when set, is the narrative.player_input or
	// narrative.dm_prose message_id that prompted this image.
	InReplyToMessageID string `json:"in_reply_to_message_id,omitempty"`
}

// NarrativeSceneImageMessage is a narrative.scene_image Message.
type NarrativeSceneImageMessage = Message[NarrativeSceneImagePayload]
