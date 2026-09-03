// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import "testing"

func TestValidateMove_WithinSpeed_Succeeds(t *testing.T) {
	g := NewGrid(10, 10)

	ok, cost := ValidateMove(g, 0, 0, 3, 0, 30) // 30 ft = 6 cells budget
	if !ok {
		t.Fatal("ValidateMove() ok = false, want true (3 cells, well within 30ft/5=6 cell budget)")
	}
	if cost != 3 {
		t.Errorf("cost = %d, want 3", cost)
	}
}

func TestValidateMove_ExceedsSpeed_Rejected(t *testing.T) {
	g := NewGrid(10, 10)

	// 30 ft = 6 cells; 8 cells straight-line exceeds that.
	ok, _ := ValidateMove(g, 0, 0, 8, 0, 30)
	if ok {
		t.Error("ValidateMove() ok = true, want false (destination beyond speed budget)")
	}
}

func TestValidateMove_DestinationBlocked_Rejected(t *testing.T) {
	g := NewGrid(5, 5)
	g.Set(2, 0, Cell{BlocksMovement: true})

	ok, _ := ValidateMove(g, 0, 0, 2, 0, 30)
	if ok {
		t.Error("ValidateMove() ok = true, want false (destination cell is a wall)")
	}
}

func TestValidateMove_NoPathAroundWall_Rejected(t *testing.T) {
	// A wall fully spans row y=1, splitting the grid top from bottom with
	// no gap — no path exists even with unlimited speed within grid
	// bounds.
	g := NewGrid(5, 5)
	for x := 0; x < 5; x++ {
		g.Set(x, 1, Cell{BlocksMovement: true})
	}

	ok, _ := ValidateMove(g, 2, 0, 2, 4, 1000)
	if ok {
		t.Error("ValidateMove() ok = true, want false (no path exists around a fully sealed wall)")
	}
}

func TestValidateMove_PathAroundWall_UsesActualPathCostNotStraightLine(t *testing.T) {
	// Wall spans row y=1 except a gap at x=0 — the only path from (2,0)
	// to (2,2) has to detour through that gap, costing more than the
	// straight-line distance of 2.
	g := NewGrid(5, 3)
	for x := 1; x < 5; x++ {
		g.Set(x, 1, Cell{BlocksMovement: true})
	}

	ok, cost := ValidateMove(g, 2, 0, 2, 2, 100)
	if !ok {
		t.Fatal("ValidateMove() ok = false, want true (a detour path exists)")
	}
	if cost <= 2 {
		t.Errorf("cost = %d, want more than the straight-line distance of 2 (must detour through the gap)", cost)
	}
}

func TestValidateMove_SameCell_SucceedsAtZeroCost(t *testing.T) {
	g := NewGrid(5, 5)

	ok, cost := ValidateMove(g, 2, 2, 2, 2, 5)
	if !ok {
		t.Fatal("ValidateMove() ok = false, want true (moving to your own current cell)")
	}
	if cost != 0 {
		t.Errorf("cost = %d, want 0", cost)
	}
}

func TestValidateMove_ZeroSpeed_RejectsAnyRealMove(t *testing.T) {
	g := NewGrid(5, 5)

	ok, _ := ValidateMove(g, 0, 0, 1, 0, 0)
	if ok {
		t.Error("ValidateMove() ok = true, want false (0 speed can't move at all)")
	}
}

func TestValidateMove_OriginOutOfBounds_Rejected(t *testing.T) {
	g := NewGrid(5, 5)

	ok, _ := ValidateMove(g, -1, -1, 2, 2, 100)
	if ok {
		t.Error("ValidateMove() ok = true, want false (origin out of bounds)")
	}
}

func TestValidateMove_NilGrid_Rejected(t *testing.T) {
	ok, _ := ValidateMove(nil, 0, 0, 1, 1, 100)
	if ok {
		t.Error("ValidateMove() ok = true, want false (nil grid)")
	}
}
