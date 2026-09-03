// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

// Token is one creature currently placed on a State's Grid.
type Token struct {
	TokenID     string
	CharacterID string
	X, Y        int
}

// State is one campaign's active combat map — the grid plus every token
// currently placed on it. The zero value is not usable; construct with
// NewState. Not safe for concurrent use — the caller (internal/server)
// owns its own locking, the same way turnOrder does for turn-order state
// (see turn_order.go's doc comment for that same pattern and its
// documented "lost on Master restart" limitation, which applies here too:
// State is in-memory only, never persisted).
type State struct {
	Grid   *Grid
	Tokens []Token
}

// NewState wraps grid with no tokens placed yet.
func NewState(grid *Grid) *State {
	return &State{Grid: grid}
}

// PlaceToken adds a new token at (x, y), replacing any existing token for
// the same CharacterID (a character can only have one token on the map at
// a time) rather than accumulating duplicates.
func (s *State) PlaceToken(tok Token) {
	for i, existing := range s.Tokens {
		if existing.CharacterID == tok.CharacterID {
			s.Tokens[i] = tok
			return
		}
	}
	s.Tokens = append(s.Tokens, tok)
}

// TokenByID returns the token with the given TokenID, and whether one was
// found.
func (s *State) TokenByID(tokenID string) (Token, bool) {
	for _, tok := range s.Tokens {
		if tok.TokenID == tokenID {
			return tok, true
		}
	}
	return Token{}, false
}

// MoveToken updates the position of the token identified by tokenID.
// Returns false if no such token exists — the caller is expected to have
// already validated the move (ValidateMove) before calling this, so a
// false return here indicates a caller bug (stale tokenID), not a normal
// rejection path.
func (s *State) MoveToken(tokenID string, x, y int) bool {
	for i, tok := range s.Tokens {
		if tok.TokenID == tokenID {
			s.Tokens[i].X = x
			s.Tokens[i].Y = y
			return true
		}
	}
	return false
}

// VisibleTokens filters tokens to only those whose position is in
// visible — the per-recipient fog-of-war filtering step shared by both the
// protocol payload builder and Render's own token filtering (which
// duplicates this check itself, since Render takes RenderToken, not
// Token — this helper is for the protocol payload side).
func VisibleTokens(tokens []Token, visible map[Position]bool) []Token {
	var result []Token
	for _, tok := range tokens {
		if visible[Position{tok.X, tok.Y}] {
			result = append(result, tok)
		}
	}
	return result
}
