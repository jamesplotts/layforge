// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import "testing"

func TestVisible_OpenRoom_SeesEveryCellWithinRadius(t *testing.T) {
	g := NewGrid(5, 5)

	visible := Visible(g, 2, 2, 10)

	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			if !visible[Position{x, y}] {
				t.Errorf("Position{%d, %d} not visible in an open room with no obstacles", x, y)
			}
		}
	}
}

func TestVisible_OriginCellAlwaysVisible_EvenIfWallSomehow(t *testing.T) {
	g := NewGrid(3, 3)
	g.Set(1, 1, Cell{BlocksMovement: true, BlocksLOS: true})

	visible := Visible(g, 1, 1, 0)

	if !visible[Position{1, 1}] {
		t.Error("origin cell not in visible set")
	}
}

func TestVisible_SingleWallBlocksTheCellDirectlyBehindIt(t *testing.T) {
	// Origin at (2, 2). Wall at (2, 1) directly north. Cell (2, 0), two
	// north of origin and directly behind the wall, must be blocked.
	g := NewGrid(5, 5)
	g.Set(2, 1, Cell{BlocksMovement: true, BlocksLOS: true})

	visible := Visible(g, 2, 2, 0)

	if visible[Position{2, 0}] {
		t.Error("Position{2, 0} visible, want blocked by the wall at {2, 1}")
	}
	// The wall cell itself is still visible (you can see a wall, you just
	// can't see past it).
	if !visible[Position{2, 1}] {
		t.Error("Position{2, 1} (the wall itself) not visible, want visible")
	}
	// A cell not behind the wall from the origin's point of view stays lit.
	if !visible[Position{0, 2}] {
		t.Error("Position{0, 2}, not behind any wall, not visible")
	}
}

func TestVisible_RadiusLimitsVisibility(t *testing.T) {
	g := NewGrid(11, 11)

	visible := Visible(g, 5, 5, 2)

	if !visible[Position{5, 5}] {
		t.Error("origin not visible")
	}
	if !visible[Position{6, 5}] {
		t.Error("Position{6, 5} (distance 1) not visible within radius 2")
	}
	if visible[Position{9, 5}] {
		t.Error("Position{9, 5} (distance 4) visible, want out of radius 2")
	}
}

func TestVisible_GapInWall_SeesThroughGapNotThroughWall(t *testing.T) {
	// A wall spans row y=1 for x=1..4, leaving a one-cell gap at x=0.
	// From origin (0, 0), directly above the gap, (0, 2) should be
	// visible straight through the gap; (3, 2), behind solid wall, should
	// not.
	g := NewGrid(5, 3)
	for x := 1; x < 5; x++ {
		g.Set(x, 1, Cell{BlocksMovement: true, BlocksLOS: true})
	}

	visible := Visible(g, 0, 0, 0)

	if !visible[Position{0, 2}] {
		t.Error("Position{0, 2}, straight through the gap at x=0, not visible, want visible")
	}
	if visible[Position{3, 2}] {
		t.Error("Position{3, 2}, behind solid wall, visible, want blocked")
	}
}

func TestVisible_NilOrOutOfBoundsOrigin_ReturnsOriginOnly(t *testing.T) {
	g := NewGrid(3, 3)

	visible := Visible(g, 99, 99, 0)

	if len(visible) != 1 || !visible[Position{99, 99}] {
		t.Errorf("Visible() with out-of-bounds origin = %v, want just the origin itself", visible)
	}
}
