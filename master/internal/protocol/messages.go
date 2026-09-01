// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

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
