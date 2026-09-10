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
	// AuthToken is the identity credential: a Discord login session token
	// when the Master runs Discord OAuth (design doc §6.6), otherwise
	// unused. It is not the room password — that is CampaignPassword, a
	// separate field so a campaign can require both.
	AuthToken string `json:"auth_token"`
	// CampaignPassword is the per-campaign room password (design doc
	// §6.6's room-code provider), when the campaign has one set. Empty
	// otherwise.
	CampaignPassword string `json:"campaign_password,omitempty"`
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
	// Identity is set on a joined state when the connection authenticated
	// to a real account (Discord OAuth), so the client can show who it is
	// signed in as. Omitted (nil) for an unauthenticated join — an open
	// campaign or a room-password-only one.
	Identity *SessionIdentity `json:"identity,omitempty"`
}

// SessionIdentity is the verified account identity of an authenticated
// connection, echoed back on the joined system.session_state. It never
// carries a token or any secret — only what a client renders. Mirrors
// auth.Identity (package protocol can't import package auth).
type SessionIdentity struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
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

// AudioChunkPayload is the payload of an audio.chunk message: one piece
// of a push-to-talk recording streamed while the button is held (design
// doc §4). StreamID groups every chunk belonging to one held-button
// session; Sequence orders them (chunks are expected to arrive in order
// over a single connection, but Sequence lets a future implementation
// tolerate reordering without a protocol change). Final is true on
// exactly the chunk sent when the button is released — that's the
// signal Master uses to stop buffering and actually transcribe, not a
// separate control message, so a partial recording never gets
// transcribed by accident. MimeType names the container/codec the
// concatenated chunks decode as (e.g. "audio/webm;codecs=opus", a
// browser MediaRecorder's typical default) — Master forwards it
// unmodified to the transcription provider rather than assuming a
// single fixed format, since that's a client/browser choice, not
// Master's to make. See protocol/asyncapi.yaml
// components.messages.AudioChunk.
type AudioChunkPayload struct {
	StreamID    string `json:"stream_id"`
	Sequence    int    `json:"sequence"`
	AudioBase64 string `json:"audio_base64"`
	Final       bool   `json:"final"`
	MimeType    string `json:"mime_type"`
}

// AudioChunkMessage is an audio.chunk Message.
type AudioChunkMessage = Message[AudioChunkPayload]

// AudioTranscriptionPayload is the payload of an audio.transcription
// message: Master's transcription of one audio.chunk stream, sent back
// to the speaking player's own connection only — never broadcast, since
// a still-in-progress or freshly finalized recording is nobody else's
// business (design doc §4). IsFinal is always true in this
// implementation: Master transcribes once, after the stream's Final
// chunk arrives, not incrementally per chunk — see
// internal/server/audio.go's own doc comment for why live partial
// transcription (design doc §4's other stated goal) is a deliberately
// deferred enhancement, not implemented by this message alone. Per
// design doc §10, this text is never written to the durable event log
// as audio.transcription itself — only once the player edits/confirms
// it and sends it as a real narrative.player_input does it become part
// of the durable log, the same as any typed input. See protocol/
// asyncapi.yaml components.messages.AudioTranscription.
type AudioTranscriptionPayload struct {
	StreamID string `json:"stream_id"`
	Text     string `json:"text"`
	IsFinal  bool   `json:"is_final"`
}

// AudioTranscriptionMessage is an audio.transcription Message.
type AudioTranscriptionMessage = Message[AudioTranscriptionPayload]

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

// CharacterReviewResultPayload is the payload of a
// character.review_result message: the outcome of the character-import
// review flow (design doc §9.4) — either the automatic post-upload pass
// (a deterministic campaign level-range check, then the DM AI's own
// balance judgment via the review_character tool — see
// internal/server/character_review.go) or a later Host decision made
// through the admin panel. Sent only to the character's own
// owner (sendToSender), never broadcast — this is a private outcome
// between Master and that one player, not table-facing narrative
// content. Distinct from character.validation_result: that message
// means "your upload parsed and was saved as pending review"; this one
// means "a review of that saved character has now concluded."
type CharacterReviewResultPayload struct {
	CharacterID string `json:"character_id"`
	// Status is store.CharacterStatusApproved or
	// store.CharacterStatusRejected — review_result is never sent for
	// CharacterStatusPendingReview, since that's not a concluded review.
	Status string `json:"status"`
	// Reason is a short, human-readable explanation — the deterministic
	// level-range gate's own plain-text message, the DM AI's stated
	// reason from its review_character tool call, or whatever the Host
	// typed into the admin panel. Empty when nobody supplied one (e.g. a
	// bare Host approval with no comment).
	Reason string `json:"reason,omitempty"`
}

// CharacterReviewResultMessage is a character.review_result Message.
type CharacterReviewResultMessage = Message[CharacterReviewResultPayload]

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

// CharacterCreationStartPayload is the payload of a
// character.creation_start message: a player, right after joining,
// asking to begin choosing/creating their character. Master replies
// with its own fixed top-level client.choice ("Import a character / Roll
// a new one / Pick a pregen") and drives the rest of the conversation
// over client.query / client.choice — this message is Master's own
// concept, not something the System Engine has any part in.
type CharacterCreationStartPayload struct {
	// CharacterName is the display name the player chose for their
	// character (the reworked join flow asks for it as the first step,
	// design doc §9.4). Used as the roll flow's character name; ignored
	// for import (the name is in the pasted JSON) and pregen (it has its
	// own). May be empty from an older client, which falls back to the
	// sender id.
	CharacterName string `json:"character_name,omitempty"`
}

// CharacterCreationStartMessage is a character.creation_start Message.
type CharacterCreationStartMessage = Message[CharacterCreationStartPayload]

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

// ToolResultPayload is the payload of a tool.result message: broadcast
// of a completed DM tool-use call, for transparency/logging (design doc
// §8). Governance-gate rejections (design doc §9) surface here as
// Success=false with a real ReasonCode — e.g. "pvp_blocked"/
// "pvp_no_consent" (§9.1's PvP policy, dmApplyEffect) and
// "character_not_approved" (§9.4's character-import review flow,
// characterMayAct in dm_tools.go) — never silently narrated around. See
// protocol/asyncapi.yaml components.messages.ToolResult.
type ToolResultPayload struct {
	ToolName string `json:"tool_name"`
	// Caller is "dm" or a specific character_id (design doc §8's logging
	// requirement) — always "dm" today, since nothing but the DM model
	// calls these tools yet.
	Caller  string `json:"caller"`
	Success bool   `json:"success"`
	// ReasonCode is set on failure, e.g. "invalid_arguments",
	// "character_not_found", "engine_error", "pvp_blocked",
	// "character_not_approved".
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

// GridCellPayload is one cell of a MapGridPayload — see that type's doc
// comment for why this is a coarser model than OpenCombatEngine's own
// CoverType/ObscurementType (design doc §6.2, internal/combatmap).
type GridCellPayload struct {
	X                int  `json:"x"`
	Y                int  `json:"y"`
	BlocksMovement   bool `json:"blocks_movement"`
	BlocksLOS        bool `json:"blocks_los"`
	DifficultTerrain bool `json:"difficult_terrain,omitempty"`
}

// MapGridPayload is the blocking-cell grid a MapTokenStatePayload carries —
// deliberately a binary blocks-movement/blocks-LOS/difficult-terrain model
// for now, not OpenCombatEngine's richer CoverType/ObscurementType: this
// grid never reaches OpenCombatEngine today (see MapTokenStatePayload's own
// doc comment), it only drives Master's own fog-of-war computation and
// rendering, so there's nothing yet to map the richer enum onto.
type MapGridPayload struct {
	Width  int               `json:"width"`
	Height int               `json:"height"`
	Cells  []GridCellPayload `json:"cells"`
}

// GridPositionPayload mirrors protocol/asyncapi.yaml's GridPosition schema
// exactly (design doc §6.2) — Facing is renderer-agnostic degrees, carried
// for a future real viewport's own camera/sprite handling; nothing in this
// version of Master reads or sets it.
type GridPositionPayload struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Facing float64 `json:"facing,omitempty"`
}

// TokenPayload is one creature's position on the map, per
// protocol/asyncapi.yaml's Token schema (design doc §6.2).
type TokenPayload struct {
	TokenID     string              `json:"token_id"`
	CharacterID string              `json:"character_id"`
	Position    GridPositionPayload `json:"position"`
}

// MapTokenStatePayload is the payload of a map.token_state message: the
// current combat-map state, re-sent in full whenever it changes (the same
// "full snapshot, not a delta" semantics as TurnStatePayload/
// CharacterStatePayload, not narrative.*'s append-to-history semantics —
// see internal/server/combat_map.go's doc comment). Unlike every other
// broadcast message in this protocol, this one is NOT sent identically to
// every connection: each recipient's Grid/Tokens/ImageURL are filtered to
// their own character's fog of war (recursive shadowcasting against the
// blocking grid, internal/combatmap's fov.go) before sending, via
// session.Hub.SendToSender rather than Broadcast — design doc §9's
// information-hiding principle (a player's own client should never receive
// map state their character can't actually see) applied to vision instead
// of GM secrets.
//
// Grid/position data never reaches OpenCombatEngine in this version — see
// this session's plan doc for why mechanically gating spell/attack range,
// line of sight, and cover against this grid is deliberately out of scope
// here; this message exists purely for tracking and display.
type MapTokenStatePayload struct {
	RoomID string         `json:"room_id"`
	Grid   MapGridPayload `json:"grid"`
	Tokens []TokenPayload `json:"tokens"`
	// ImageURL is a data: URL of this recipient's own composited PNG view
	// (grid + their currently-visible tokens, fog already applied) —
	// Master renders it directly (Go stdlib image/png), no external
	// service, unlike client.image's ImageURL which points at a
	// configured imagegen.Provider.
	ImageURL string `json:"image_url"`
}

// MapTokenStateMessage is a map.token_state Message.
type MapTokenStateMessage = Message[MapTokenStatePayload]

// MapTokenMoveRequestPayload is the payload of a map.token_move_request
// message: a player asking to move their own character's token to a new
// cell (design doc §6.2). Master validates ownership, movement speed, and
// the blocking grid before accepting — see
// internal/server/combat_map.go's handler.
type MapTokenMoveRequestPayload struct {
	TokenID string              `json:"token_id"`
	To      GridPositionPayload `json:"to"`
}

// MapTokenMoveRequestMessage is a map.token_move_request Message.
type MapTokenMoveRequestMessage = Message[MapTokenMoveRequestPayload]

// VehicleImportPayload is the payload of a vehicle.import message: a
// player declaring a new mount/cart/wagon/ship the party now has (design
// doc §6.4's "off-site possessions (mounts, stashes)"). No mechanical
// schema — a vehicle is never a character/creature record (see
// internal/server/vehicles.go's own doc comment) — Name/VehicleType are
// free text.
type VehicleImportPayload struct {
	Name        string `json:"name"`
	VehicleType string `json:"vehicle_type"`
}

// VehicleImportMessage is a vehicle.import Message.
type VehicleImportMessage = Message[VehicleImportPayload]

// VehicleImportedPayload is the payload of a vehicle.imported message —
// broadcast to the whole campaign whenever a new vehicle is added,
// whether via a real vehicle.import or the DM's own acquire_vehicle
// tool, so every client learns about a new shared vehicle the same way
// regardless of which path created it.
type VehicleImportedPayload struct {
	VehicleID   string `json:"vehicle_id"`
	Name        string `json:"name"`
	VehicleType string `json:"vehicle_type"`
}

// VehicleImportedMessage is a vehicle.imported Message.
type VehicleImportedMessage = Message[VehicleImportedPayload]

// TermsAcceptPayload is the payload of a terms.accept message — a
// client accepting the current terms.Version (see internal/terms and
// internal/server's dispatch gate) for this connection. Version is
// echoed back by the client (rather than assumed) so a stale cached
// client bundle gets a clear terms_version_mismatch system.error
// instead of silently "succeeding" against an outdated text.
type TermsAcceptPayload struct {
	Version string `json:"version"`
}

// TermsAcceptMessage is a terms.accept Message.
type TermsAcceptMessage = Message[TermsAcceptPayload]

// --- The client.* family (design doc §4) -----------------------------------
//
// The small, reusable set of chat-bubble interactions Master uses to talk
// to a player. Every one is a bubble in the client's main message area:
// a one-way text bubble (client.display), a typed-answer prompt
// (client.query), a pick-one prompt (client.choice), an image
// (client.image), and the dice-roll exchange (client.roll and friends).
// Character creation is built entirely out of client.query / client.choice
// — it is not a separate prompt mechanism.

// ClientDisplayPayload is the payload of a client.display message: a
// text bubble shown to a player, or to the whole table. This is the DM's
// narration/description output — mechanical resolutions read back to the
// player ("Reorx hits the bugbear, 14+6=20, 9 damage"), scene-setting,
// NPC dialogue.
type ClientDisplayPayload struct {
	// Recipient is the sender_id/account the bubble is for; empty means
	// broadcast to every client in the campaign. A non-empty Recipient is
	// how per-player narration (design doc §9.7's knowledge scoping) is
	// delivered — only that connection ever receives the message.
	Recipient string `json:"recipient,omitempty"`
	Text      string `json:"text"`
	// InReplyToMessageID is the message_id of the player input this
	// responds to, if any.
	InReplyToMessageID string `json:"in_reply_to_message_id,omitempty"`
	// Visibility, when set to a private scope, is what the persisted event
	// log carries so log.history_request can filter this bubble out for
	// everyone it wasn't for (design doc §9.7) — used by narrate_privately,
	// which delivers one identical bubble to several recipients' own
	// connections and records it once with this scope. nil (omitted) for an
	// ordinary broadcast or a single-Recipient send.
	Visibility *VisibilityScope `json:"visibility,omitempty"`
}

// ClientDisplayMessage is a client.display Message.
type ClientDisplayMessage = Message[ClientDisplayPayload]

// ClientQueryPayload is the payload of a client.query message: a bubble
// asking the player to type a free-text answer. Rendered as PromptText, a
// single-line input (labeled InputLabel, placeholder InputPlaceholder),
// and a submit button reading SubmitLabel. The player replies with a
// client.query_response carrying the same PromptID.
type ClientQueryPayload struct {
	// PromptID is Master-generated; the player's client.query_response
	// echoes it so Master can route the answer back to whatever asked.
	PromptID   string `json:"prompt_id"`
	PromptText string `json:"prompt_text"`
	// InputLabel/InputPlaceholder/SubmitLabel are optional UI hints; a
	// client renders sensible defaults (e.g. "Send") when they're empty.
	InputLabel       string `json:"input_label,omitempty"`
	InputPlaceholder string `json:"input_placeholder,omitempty"`
	SubmitLabel      string `json:"submit_label,omitempty"`
	// AcceptsFileUpload tells the client to offer a file picker alongside
	// the text input — set only when the expected answer is a document
	// (the character-import "paste your character JSON" step).
	AcceptsFileUpload bool `json:"accepts_file_upload,omitempty"`
}

// ClientQueryMessage is a client.query Message.
type ClientQueryMessage = Message[ClientQueryPayload]

// ClientQueryResponsePayload is the payload of a client.query_response
// message: the player's typed answer to the client.query with the
// matching PromptID. A response whose PromptID has no pending query is a
// real rejection (system.error), not a guess.
type ClientQueryResponsePayload struct {
	PromptID string `json:"prompt_id"`
	Text     string `json:"text"`
}

// ClientQueryResponseMessage is a client.query_response Message.
type ClientQueryResponseMessage = Message[ClientQueryResponsePayload]

// ClientChoiceOption is one selectable option in a client.choice bubble.
// Value is what comes back in the client.choice_response; Label is the
// button text. They're separate so two options can share a display name
// (e.g. two pregenerated characters both called "Bram") without the
// response being ambiguous.
type ClientChoiceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ClientChoicePayload is the payload of a client.choice message: a bubble
// asking the player to pick one of Options, rendered as PromptText plus
// one button per option. The player replies with a client.choice_response
// carrying the same PromptID and the chosen option's Value.
type ClientChoicePayload struct {
	PromptID   string               `json:"prompt_id"`
	PromptText string               `json:"prompt_text"`
	Options    []ClientChoiceOption `json:"options"`
}

// ClientChoiceMessage is a client.choice Message.
type ClientChoiceMessage = Message[ClientChoicePayload]

// ClientChoiceResponsePayload is the payload of a client.choice_response
// message: the Value of the option the player picked from the
// client.choice with the matching PromptID. Same "unknown PromptID is a
// rejection" rule as client.query_response.
type ClientChoiceResponsePayload struct {
	PromptID string `json:"prompt_id"`
	Value    string `json:"value"`
}

// ClientChoiceResponseMessage is a client.choice_response Message.
type ClientChoiceResponseMessage = Message[ClientChoiceResponsePayload]

// ClientImagePayload is the payload of a client.image message: an image
// shown in a bubble, to a player or the whole table. Master neither
// authors nor stores the image — ImageURL points at wherever the
// configured imagegen.Provider hosts it.
type ClientImagePayload struct {
	// Recipient is the sender_id/account the image is for; empty means
	// broadcast — same semantics as ClientDisplayPayload.Recipient.
	Recipient string `json:"recipient,omitempty"`
	ImageURL  string `json:"image_url"`
	Caption   string `json:"caption,omitempty"`
	// Prompt is the description actually sent to the image generator
	// (including any maturity-tier constraint appended to it, design doc
	// §9.5) — surfaced for transparency, the same reasoning as tool.result
	// logging every DM tool call.
	Prompt string `json:"prompt,omitempty"`
	// InReplyToMessageID, when set, is the message_id that prompted this
	// image.
	InReplyToMessageID string `json:"in_reply_to_message_id,omitempty"`
}

// ClientImageMessage is a client.image Message.
type ClientImageMessage = Message[ClientImagePayload]

// ClientRollPurpose names what a client.roll is for, so a client can
// label/style the bubble ("Attack roll", "Damage") without parsing prose.
// The zero value, ClientRollPurposeUnspecified, is never valid on the
// wire.
type ClientRollPurpose string

const (
	ClientRollPurposeUnspecified ClientRollPurpose = ""
	ClientRollPurposeAttack      ClientRollPurpose = "attack"
	ClientRollPurposeDamage      ClientRollPurpose = "damage"
	ClientRollPurposeSave        ClientRollPurpose = "save"
	ClientRollPurposeCheck       ClientRollPurpose = "check"
	ClientRollPurposeInitiative  ClientRollPurpose = "initiative"
	ClientRollPurposeDeathSave   ClientRollPurpose = "death_save"
	ClientRollPurposeCustom      ClientRollPurpose = "custom"
)

// IsValid reports whether p is a recognized roll purpose. It deliberately
// returns false for ClientRollPurposeUnspecified.
func (p ClientRollPurpose) IsValid() bool {
	switch p {
	case ClientRollPurposeAttack, ClientRollPurposeDamage, ClientRollPurposeSave,
		ClientRollPurposeCheck, ClientRollPurposeInitiative, ClientRollPurposeDeathSave,
		ClientRollPurposeCustom:
		return true
	default:
		return false
	}
}

// ClientRollDie is one die in a client.roll. ID is unique within the
// roll (the client.roll_reveal that follows a click names it). Sides is
// the die size (4, 6, 8, 10, 12, 20, 100). Label is an optional
// per-die caption ("1d8 slashing", "sneak attack"). Result is the
// engine's authoritative face value — present in client.roll (sent only
// to the roller, hidden inside an unrevealed outline) and in
// client.roll_spectate_reveal, absent from client.roll_spectate.
type ClientRollDie struct {
	ID     string `json:"id"`
	Sides  int    `json:"sides"`
	Label  string `json:"label,omitempty"`
	Result int    `json:"result,omitempty"`
}

// ClientRollPayload is the payload of a client.roll message: sent to the
// player who has to roll. The client shows one clickable outline per die;
// each click reveals that die's already-decided Result with a tumble
// animation. When every die is revealed the client sends a
// client.roll_result... actually no — see the reveal sequence below:
// each click sends a client.roll_reveal, and Master emits
// client.roll_complete once all are in.
//
// NOTE: nothing in Master emits or consumes client.roll* yet — a
// follow-up (a Fable subagent) builds the interactive dice UI, the
// per-die reveal relay, and the DM-slow-pass "wait for the roll" gate.
// The shapes are specced now so that follow-up doesn't ship a protocol
// migration. See docs and the plan for the full design.
type ClientRollPayload struct {
	PromptID    string            `json:"prompt_id"`
	CharacterID string            `json:"character_id"`
	Purpose     ClientRollPurpose `json:"purpose"`
	// Text is the prompt shown above the dice ("Roll to attack with
	// Reorx's battle axe").
	Text string          `json:"text"`
	Dice []ClientRollDie `json:"dice"`
}

// ClientRollMessage is a client.roll Message.
type ClientRollMessage = Message[ClientRollPayload]

// ClientRollSpectatePayload is the payload of a client.roll_spectate
// message: the read-only twin of client.roll, sent to every *other*
// client in the campaign. Its Dice carry no Result — spectators can't
// see the numbers until the roller reveals them (design doc §9.7). Each
// ghost die is filled in by a following client.roll_spectate_reveal.
type ClientRollSpectatePayload struct {
	PromptID    string            `json:"prompt_id"`
	CharacterID string            `json:"character_id"`
	Purpose     ClientRollPurpose `json:"purpose"`
	Text        string            `json:"text"`
	Dice        []ClientRollDie   `json:"dice"`
}

// ClientRollSpectateMessage is a client.roll_spectate Message.
type ClientRollSpectateMessage = Message[ClientRollSpectatePayload]

// ClientRollRevealPayload is the payload of a client.roll_reveal
// message: the roller telling Master they just clicked (and revealed)
// one die of PromptID. Master looks up that die's authoritative result
// and relays it to spectators as client.roll_spectate_reveal.
type ClientRollRevealPayload struct {
	PromptID string `json:"prompt_id"`
	DieID    string `json:"die_id"`
}

// ClientRollRevealMessage is a client.roll_reveal Message.
type ClientRollRevealMessage = Message[ClientRollRevealPayload]

// ClientRollSpectateRevealPayload is the payload of a
// client.roll_spectate_reveal message: Master relaying one revealed die
// to the spectator clients, so their ghost die turns into the result
// die at the same moment the roller sees it.
type ClientRollSpectateRevealPayload struct {
	PromptID string `json:"prompt_id"`
	DieID    string `json:"die_id"`
	Result   int    `json:"result"`
}

// ClientRollSpectateRevealMessage is a client.roll_spectate_reveal Message.
type ClientRollSpectateRevealMessage = Message[ClientRollSpectateRevealPayload]

// ClientRollCompletePayload is the payload of a client.roll_complete
// message: broadcast to the whole campaign once every die of PromptID
// has been revealed. It is also the signal that unblocks the DM's
// slow pass, which was waiting for the roll to finish before narrating.
type ClientRollCompletePayload struct {
	PromptID string `json:"prompt_id"`
	Total    int    `json:"total"`
	// ResultSummary is an engine-set characterization ("Hit", "Critical
	// hit", "9 slashing damage") when one is available.
	ResultSummary string `json:"result_summary,omitempty"`
}

// ClientRollCompleteMessage is a client.roll_complete Message.
type ClientRollCompleteMessage = Message[ClientRollCompletePayload]
