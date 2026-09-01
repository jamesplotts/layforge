// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

// SessionState is the lifecycle state carried by a system.session_state
// message. The zero value, SessionStateUnspecified, is never valid on
// the wire — see IsValid.
type SessionState string

// Recognized session states, matching protocol/asyncapi.yaml's
// SystemSessionState message. Only SessionStateJoined is emitted by
// Master today; the others are declared now per CLAUDE.md's "design
// fields forward" rule so the wire shape doesn't need to change when
// Master starts emitting them.
const (
	SessionStateUnspecified SessionState = ""
	SessionStateJoined      SessionState = "joined"
	SessionStateLeft        SessionState = "left"
	SessionStatePaused      SessionState = "paused"
	SessionStateResumed     SessionState = "resumed"
)

// IsValid reports whether s is a recognized session state. It
// deliberately returns false for SessionStateUnspecified.
func (s SessionState) IsValid() bool {
	switch s {
	case SessionStateJoined, SessionStateLeft, SessionStatePaused, SessionStateResumed:
		return true
	default:
		return false
	}
}
