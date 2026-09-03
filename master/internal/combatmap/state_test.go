// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import "testing"

func TestState_PlaceToken_NewCharacter_Adds(t *testing.T) {
	s := NewState(NewGrid(5, 5))

	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 1, Y: 1})

	if len(s.Tokens) != 1 {
		t.Fatalf("len(Tokens) = %d, want 1", len(s.Tokens))
	}
}

func TestState_PlaceToken_ExistingCharacter_ReplacesNotDuplicates(t *testing.T) {
	s := NewState(NewGrid(5, 5))
	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 1, Y: 1})

	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 3, Y: 3})

	if len(s.Tokens) != 1 {
		t.Fatalf("len(Tokens) = %d, want 1 (re-placing the same character must replace, not duplicate)", len(s.Tokens))
	}
	if s.Tokens[0].X != 3 || s.Tokens[0].Y != 3 {
		t.Errorf("Tokens[0] position = (%d, %d), want (3, 3)", s.Tokens[0].X, s.Tokens[0].Y)
	}
}

func TestState_TokenByID_Found(t *testing.T) {
	s := NewState(NewGrid(5, 5))
	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 2, Y: 2})

	got, ok := s.TokenByID("tok-1")
	if !ok {
		t.Fatal("TokenByID() ok = false, want true")
	}
	if got.X != 2 || got.Y != 2 {
		t.Errorf("got = %+v, want X=2 Y=2", got)
	}
}

func TestState_TokenByID_NotFound(t *testing.T) {
	s := NewState(NewGrid(5, 5))

	if _, ok := s.TokenByID("nope"); ok {
		t.Error("TokenByID() ok = true, want false")
	}
}

func TestState_TokenByCharacterID_Found(t *testing.T) {
	s := NewState(NewGrid(5, 5))
	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 2, Y: 3})

	got, ok := s.TokenByCharacterID("char-1")
	if !ok {
		t.Fatal("TokenByCharacterID() ok = false, want true")
	}
	if got.X != 2 || got.Y != 3 {
		t.Errorf("got = %+v, want X=2 Y=3", got)
	}
}

func TestState_TokenByCharacterID_NotFound(t *testing.T) {
	s := NewState(NewGrid(5, 5))

	if _, ok := s.TokenByCharacterID("nope"); ok {
		t.Error("TokenByCharacterID() ok = true, want false")
	}
}

func TestState_MoveToken_ExistingToken_UpdatesPosition(t *testing.T) {
	s := NewState(NewGrid(5, 5))
	s.PlaceToken(Token{TokenID: "tok-1", CharacterID: "char-1", X: 0, Y: 0})

	ok := s.MoveToken("tok-1", 4, 4)

	if !ok {
		t.Fatal("MoveToken() = false, want true")
	}
	got, _ := s.TokenByID("tok-1")
	if got.X != 4 || got.Y != 4 {
		t.Errorf("position after move = (%d, %d), want (4, 4)", got.X, got.Y)
	}
}

func TestState_MoveToken_UnknownToken_ReturnsFalse(t *testing.T) {
	s := NewState(NewGrid(5, 5))

	if ok := s.MoveToken("nope", 1, 1); ok {
		t.Error("MoveToken() = true, want false")
	}
}

func TestVisibleTokens_FiltersToOnlyVisiblePositions(t *testing.T) {
	tokens := []Token{
		{TokenID: "a", CharacterID: "char-a", X: 0, Y: 0},
		{TokenID: "b", CharacterID: "char-b", X: 5, Y: 5},
	}
	visible := map[Position]bool{{0, 0}: true}

	got := VisibleTokens(tokens, visible)

	if len(got) != 1 || got[0].TokenID != "a" {
		t.Errorf("VisibleTokens() = %+v, want only token %q", got, "a")
	}
}

func TestVisibleTokens_NoneVisible_ReturnsEmpty(t *testing.T) {
	tokens := []Token{{TokenID: "a", CharacterID: "char-a", X: 0, Y: 0}}
	visible := map[Position]bool{{9, 9}: true}

	got := VisibleTokens(tokens, visible)

	if len(got) != 0 {
		t.Errorf("VisibleTokens() = %+v, want empty", got)
	}
}
