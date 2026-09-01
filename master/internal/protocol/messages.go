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
// doc §10) for chat-log/history review (§11). See protocol/asyncapi.yaml
// components.messages.LogHistoryRequest.
type HistoryRequestPayload struct {
	// AfterSequence returns only events after this store-assigned
	// sequence number — not a message_id or timestamp; see design doc
	// §10 on why sequence, not client-supplied fields, is the pagination
	// cursor. Zero means from the beginning of the campaign's log.
	AfterSequence int64 `json:"after_sequence,omitempty"`
	// Limit caps how many events come back; zero (or an oversized value)
	// falls back to Master's own default/cap.
	Limit int `json:"limit,omitempty"`
}

// HistoryRequestMessage is a log.history_request Message.
type HistoryRequestMessage = Message[HistoryRequestPayload]

// HistoryResponsePayload is the payload of a log.history_response
// message: a page of previously recorded messages, each returned exactly
// as it was originally sent — not re-wrapped in any further envelope, so
// a client renders history the same way it renders anything live. See
// protocol/asyncapi.yaml components.messages.LogHistoryResponse.
type HistoryResponsePayload struct {
	Events []json.RawMessage `json:"events"`
	// NextAfterSequence is the AfterSequence to pass on the next request
	// to continue paging; zero/omitted when Events is empty.
	NextAfterSequence int64 `json:"next_after_sequence,omitempty"`
	HasMore           bool  `json:"has_more"`
}

// HistoryResponseMessage is a log.history_response Message.
type HistoryResponseMessage = Message[HistoryResponsePayload]
