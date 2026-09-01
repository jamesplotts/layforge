// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newMessageID returns a random, sufficiently-unique message_id for a
// Master-originated message. It need not be a UUID — protocol/
// asyncapi.yaml only requires message_id to be unique enough for ack/
// dedup, not any particular format.
func newMessageID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("server: generating message id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
