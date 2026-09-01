// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package protocol

// VisibilityScopeKind is the knowledge-scoping value carried by a
// VisibilityScope (design doc §9.7: `shared_knowledge: strict |
// party_omniscient`). Nothing in Master enforces scoping yet — there is
// no campaign-pack shared_knowledge setting to drive it — but the wire
// shape is specified now so a future consumer doesn't need a protocol
// migration once that governance gate exists. The zero value,
// VisibilityScopeUnspecified, is never valid on the wire.
type VisibilityScopeKind string

const (
	VisibilityScopeUnspecified VisibilityScopeKind = ""
	VisibilityScopePublic      VisibilityScopeKind = "public"
	VisibilityScopePrivate     VisibilityScopeKind = "private"
)

// IsValid reports whether v is a recognized visibility scope. It
// deliberately returns false for VisibilityScopeUnspecified.
func (v VisibilityScopeKind) IsValid() bool {
	switch v {
	case VisibilityScopePublic, VisibilityScopePrivate:
		return true
	default:
		return false
	}
}

// VisibilityScope is knowledge-scoping metadata (design doc §9.7),
// persisted with the same scope it had live so history review can't leak
// something that was private in the moment. See protocol/asyncapi.yaml
// components.schemas.VisibilityScope.
type VisibilityScope struct {
	Scope VisibilityScopeKind `json:"scope"`
	// VisibleToCharacterIDs is set only when Scope is
	// VisibilityScopePrivate.
	VisibleToCharacterIDs []string `json:"visible_to_character_ids,omitempty"`
}
