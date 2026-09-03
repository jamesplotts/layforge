// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import "testing"

func TestNewGrid_EveryCellStartsOpenFloor(t *testing.T) {
	g := NewGrid(3, 2)

	if len(g.Cells) != 6 {
		t.Fatalf("len(Cells) = %d, want 6", len(g.Cells))
	}
	for i, cell := range g.Cells {
		if cell != (Cell{}) {
			t.Errorf("Cells[%d] = %+v, want zero value", i, cell)
		}
	}
}

func TestGrid_InBounds(t *testing.T) {
	g := NewGrid(3, 2)

	tests := []struct {
		name    string
		x, y    int
		inBound bool
	}{
		{"origin", 0, 0, true},
		{"max_corner", 2, 1, true},
		{"negative_x", -1, 0, false},
		{"negative_y", 0, -1, false},
		{"x_too_large", 3, 0, false},
		{"y_too_large", 0, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.InBounds(tt.x, tt.y); got != tt.inBound {
				t.Errorf("InBounds(%d, %d) = %v, want %v", tt.x, tt.y, got, tt.inBound)
			}
		})
	}
}

func TestGrid_SetAndAt_RoundTrips(t *testing.T) {
	g := NewGrid(3, 3)
	wall := Cell{BlocksMovement: true, BlocksLOS: true}
	g.Set(1, 1, wall)

	got, ok := g.At(1, 1)
	if !ok {
		t.Fatal("At(1, 1) ok = false, want true")
	}
	if got != wall {
		t.Errorf("At(1, 1) = %+v, want %+v", got, wall)
	}
}

func TestGrid_At_OutOfBounds_ReturnsNotOK(t *testing.T) {
	g := NewGrid(2, 2)

	if _, ok := g.At(5, 5); ok {
		t.Error("At(5, 5) ok = true, want false")
	}
}

func TestGrid_Set_OutOfBounds_NoOp(t *testing.T) {
	g := NewGrid(2, 2)

	// Must not panic.
	g.Set(-1, -1, Cell{BlocksMovement: true})
	g.Set(99, 99, Cell{BlocksMovement: true})

	for i, cell := range g.Cells {
		if cell != (Cell{}) {
			t.Errorf("Cells[%d] = %+v after an out-of-bounds Set, want unchanged zero value", i, cell)
		}
	}
}

func TestGrid_Open(t *testing.T) {
	g := NewGrid(2, 1)
	g.Set(1, 0, Cell{BlocksMovement: true})

	if !g.Open(0, 0) {
		t.Error("Open(0, 0) = false, want true (open floor)")
	}
	if g.Open(1, 0) {
		t.Error("Open(1, 0) = true, want false (blocked)")
	}
	if g.Open(9, 9) {
		t.Error("Open(9, 9) = true, want false (out of bounds)")
	}
}
