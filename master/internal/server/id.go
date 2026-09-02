// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newRandomID returns a random, sufficiently-unique hex-encoded
// identifier. It backs both newMessageID and newCharacterID: neither
// protocol/asyncapi.yaml (message_id) nor design doc §9.4 (character_id)
// requires any particular ID format, just uniqueness, so both reuse the
// same generator rather than each hand-rolling one.
func newRandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("server: generating random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newMessageID returns a random, sufficiently-unique message_id for a
// Master-originated message.
func newMessageID() (string, error) {
	return newRandomID()
}

// newCharacterID returns a random, sufficiently-unique character_id for a
// newly-imported character (design doc §9.4) — distinct from anything the
// system engine calls an actor_id (store.Character.ID's doc comment).
func newCharacterID() (string, error) {
	return newRandomID()
}
