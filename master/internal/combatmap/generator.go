// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import "math/rand"

// GenerateOptions controls GenerateRoomsAndCorridors.
type GenerateOptions struct {
	Width, Height int
	// RoomAttempts is how many candidate rooms are tried — not how many
	// are guaranteed to land, since a candidate overlapping an existing
	// room (plus a 1-cell margin) is discarded. A larger grid needs more
	// attempts to fill comparably; there's no "attempts per area" formula
	// here, callers pick a number and can inspect the returned room count.
	RoomAttempts int
	// MinRoomSize/MaxRoomSize bound each room's width and height
	// (independently rolled per axis), inclusive.
	MinRoomSize, MaxRoomSize int
	// Rand is the source of randomness. Required — callers needing
	// deterministic output (tests) pass rand.New(rand.NewSource(seed)); a
	// nil Rand panics rather than silently falling back to a
	// process-global source, since that would make "deterministic for
	// tests" an easy trap to fall into by omission.
	Rand *rand.Rand
}

// room is a generation-time-only rectangle; the public Grid this produces
// has no notion of "rooms," only cells — Room boundaries aren't needed
// after generation (movement/FOV/rendering all work purely off Grid.Cells).
type room struct {
	x, y, w, h int
}

func (r room) center() (int, int) {
	return r.x + r.w/2, r.y + r.h/2
}

// overlaps reports whether r and other are within margin cells of each
// other on any axis — used to keep generated rooms from touching (a
// 1-cell wall always separates two rooms that aren't deliberately
// corridor-connected).
func (r room) overlaps(other room, margin int) bool {
	return r.x-margin < other.x+other.w &&
		r.x+r.w+margin > other.x &&
		r.y-margin < other.y+other.h &&
		r.y+r.h+margin > other.y
}

// GenerateRoomsAndCorridors produces a Grid of non-overlapping rectangular
// rooms connected in placement order by 1-cell-wide L-shaped corridors —
// every room is guaranteed reachable from every other, since each new room
// connects to the room immediately before it, forming a connected chain.
// This is the classic "rooms + corridors" roguelike-dungeon shape (the
// same family of algorithm popularized by the libtcod roguelike tutorial
// and Bob Nystrom's "Rooms and Mazes" writeup, though this implementation
// skips maze-filling the leftover space between rooms — every non-room,
// non-corridor cell is simply a wall, which is simpler, still fully
// connected, and closer to what a tactical single-encounter combat map
// actually needs than a sprawling full-dungeon maze). Everything not
// carved as a room or corridor is a wall (BlocksMovement and BlocksLOS
// both true).
//
// Returns nil if opts.Width/Height/RoomAttempts/MaxRoomSize aren't all
// positive, or if opts.Rand is nil — a malformed request, not a
// generation failure to recover from.
func GenerateRoomsAndCorridors(opts GenerateOptions) *Grid {
	if opts.Width <= 0 || opts.Height <= 0 || opts.RoomAttempts <= 0 || opts.MaxRoomSize <= 0 || opts.Rand == nil {
		return nil
	}
	minSize := opts.MinRoomSize
	if minSize <= 0 {
		minSize = 1
	}
	if minSize > opts.MaxRoomSize {
		minSize = opts.MaxRoomSize
	}

	g := NewGrid(opts.Width, opts.Height)
	for i := range g.Cells {
		g.Cells[i] = Cell{BlocksMovement: true, BlocksLOS: true}
	}

	var placed []room
	for i := 0; i < opts.RoomAttempts; i++ {
		w := minSize + opts.Rand.Intn(opts.MaxRoomSize-minSize+1)
		h := minSize + opts.Rand.Intn(opts.MaxRoomSize-minSize+1)
		if w >= opts.Width-1 || h >= opts.Height-1 {
			continue
		}
		x := 1 + opts.Rand.Intn(opts.Width-w-1)
		y := 1 + opts.Rand.Intn(opts.Height-h-1)
		candidate := room{x: x, y: y, w: w, h: h}

		overlapsExisting := false
		for _, other := range placed {
			if candidate.overlaps(other, 1) {
				overlapsExisting = true
				break
			}
		}
		if overlapsExisting {
			continue
		}

		carveRoom(g, candidate)
		if len(placed) > 0 {
			carveCorridor(g, opts.Rand, placed[len(placed)-1], candidate)
		}
		placed = append(placed, candidate)
	}

	return g
}

func carveRoom(g *Grid, r room) {
	for y := r.y; y < r.y+r.h; y++ {
		for x := r.x; x < r.x+r.w; x++ {
			g.Set(x, y, Cell{})
		}
	}
}

// carveCorridor carves a 1-cell-wide L-shaped path between from's and to's
// centers — horizontal leg then vertical leg, or vice versa, chosen at
// random so corridors don't all bend the same way.
func carveCorridor(g *Grid, rnd *rand.Rand, from, to room) {
	x1, y1 := from.center()
	x2, y2 := to.center()

	if rnd.Intn(2) == 0 {
		carveHorizontal(g, x1, x2, y1)
		carveVertical(g, y1, y2, x2)
	} else {
		carveVertical(g, y1, y2, x1)
		carveHorizontal(g, x1, x2, y2)
	}
}

func carveHorizontal(g *Grid, x1, x2, y int) {
	if x2 < x1 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		g.Set(x, y, Cell{})
	}
}

func carveVertical(g *Grid, y1, y2, x int) {
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		g.Set(x, y, Cell{})
	}
}
