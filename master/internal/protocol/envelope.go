// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package protocol implements the wire types for Layforge's client-facing
// WebSocket protocol, as specified in protocol/asyncapi.yaml. It is a
// thin, transport-independent layer: JSON message shapes and envelope
// validation only — no network or session logic, which belongs in
// package server.
package protocol

import (
	"errors"
	"fmt"
	"time"
)

// CurrentProtocolVersion is the protocol_version this build of Master
// speaks. A message whose envelope declares a different version fails
// Envelope.Validate — see design doc §5.
const CurrentProtocolVersion = "0.1.0"

// Envelope holds the fields every protocol message carries regardless of
// its Type: protocol_version, message_id, timestamp, sender_id, and
// campaign_id. See protocol/asyncapi.yaml's Envelope schema and design
// doc §5 for why each field is required even where it looks redundant
// given connection context — message editing, audit logging, and replay/
// spectator mode all need to address a specific message independent of
// arrival order.
type Envelope struct {
	ProtocolVersion string      `json:"protocol_version"`
	MessageID       string      `json:"message_id"`
	Timestamp       time.Time   `json:"timestamp"`
	SenderID        string      `json:"sender_id"`
	CampaignID      string      `json:"campaign_id"`
	Type            MessageType `json:"type"`
}

// Errors returned by Envelope.Validate. Callers that need to distinguish
// failure reasons (e.g. to pick a SystemErrorPayload.Code) should use
// errors.Is against these rather than comparing error strings.
var (
	ErrUnsupportedProtocolVersion = errors.New("protocol: unsupported protocol_version")
	ErrMissingMessageID           = errors.New("protocol: message_id is required")
	ErrMissingTimestamp           = errors.New("protocol: timestamp is required")
	ErrMissingSenderID            = errors.New("protocol: sender_id is required")
	ErrMissingCampaignID          = errors.New("protocol: campaign_id is required")
	ErrUnrecognizedType           = errors.New("protocol: type is not a recognized message type")
)

// Validate checks that e carries every required field and that its Type
// and ProtocolVersion are ones this build understands. It does not
// validate payload contents — callers decode and validate the payload
// for e.Type separately, since the payload shape depends on Type.
func (e Envelope) Validate() error {
	if e.ProtocolVersion != CurrentProtocolVersion {
		return fmt.Errorf("%w: got %q, want %q", ErrUnsupportedProtocolVersion, e.ProtocolVersion, CurrentProtocolVersion)
	}
	if e.MessageID == "" {
		return ErrMissingMessageID
	}
	if e.Timestamp.IsZero() {
		return ErrMissingTimestamp
	}
	if e.SenderID == "" {
		return ErrMissingSenderID
	}
	if e.CampaignID == "" {
		return ErrMissingCampaignID
	}
	if !e.Type.IsValid() {
		return fmt.Errorf("%w: %q", ErrUnrecognizedType, e.Type)
	}
	return nil
}
