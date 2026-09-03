// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package combatmap generates a combat encounter's grid map, tracks token
// (character) positions on it, computes each character's own line-of-sight
// fog of war, validates movement against that grid, and renders a
// recipient's currently-visible view as a PNG (design doc §6.2).
//
// This package is deliberately Master-only and does not talk to the
// System Engine: OpenCombatEngine's own IGridManager/StandardGridManager
// already implements a full spatial engine (distance, line of sight,
// obstacles, pathfinding, GetCreaturesInShape for AOE — see
// StandardGridManager.cs), but grid/position data is never round-tripped
// through the gRPC boundary today (Actor.character_data has no position
// field, and the System Engine proto's own comment on CastSpell says the
// contract doesn't carry positional data yet). Wiring this package's grid
// into gRPC calls so OpenCombatEngine's own range/line-of-sight/cover logic
// mechanically gates spells and attacks is real, separate, cross-repo work
// — deliberately out of scope here. This package exists to make combat
// tracking, fog of war, and a picture to look at real today, the same
// "informational now, hard-gated later" staging this session already used
// for spellcasting itself (see dm_tools.go's dmCastSpell history).
package combatmap

// Cell is one tile of a Grid. The zero value (all false) is open, walkable,
// fully visible ground — the common case, so a freshly-allocated []Cell
// slice needs no initialization for open floor.
type Cell struct {
	// BlocksMovement is true for a wall or other impassable obstacle — a
	// token cannot move into or through this cell (movement.go).
	BlocksMovement bool
	// BlocksLOS is true for a cell that blocks line of sight through it —
	// almost always identical to BlocksMovement (a wall blocks both), but
	// kept as a separate flag since they're conceptually distinct SRD
	// concepts (e.g. a closed but breakable door blocks movement without
	// blocking sight through a window in it — not modeled by this
	// generator today, but the field exists so a future generator or
	// hand-authored map can express it without a schema change).
	BlocksLOS bool
	// DifficultTerrain doubles movement cost into this cell (SRD "difficult
	// terrain") without blocking movement or sight entirely. Not produced
	// by the current generator (generator.go) — reserved for a future
	// generator/cell-type addition, same "design fields forward" reasoning
	// as everywhere else in this codebase (CLAUDE.md).
	DifficultTerrain bool
}

// Grid is a rectangular combat map: Width*Height cells, row-major (index =
// y*Width + x). The zero value is not usable — construct via NewGrid or a
// generator (generator.go).
type Grid struct {
	Width, Height int
	Cells         []Cell
}

// NewGrid returns a Width*Height Grid with every cell open floor (the Cell
// zero value) — a generator (generator.go) then carves walls into it, or a
// caller can build a hand-authored layout directly for tests.
func NewGrid(width, height int) *Grid {
	return &Grid{Width: width, Height: height, Cells: make([]Cell, width*height)}
}

// InBounds reports whether (x, y) is a real cell of g.
func (g *Grid) InBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.Width && y < g.Height
}

// At returns the cell at (x, y) and whether that position is in bounds — a
// caller reads the returned Cell only when ok is true, matching Go's own
// comma-ok map-lookup convention rather than panicking or returning a
// meaningless zero Cell silently.
func (g *Grid) At(x, y int) (cell Cell, ok bool) {
	if !g.InBounds(x, y) {
		return Cell{}, false
	}
	return g.Cells[y*g.Width+x], true
}

// Set writes cell at (x, y). A no-op if (x, y) is out of bounds, rather
// than panicking — callers carving rooms/corridors during generation
// routinely compute positions near a boundary; failing loudly there would
// make every generator call site carry its own bounds check for no
// benefit.
func (g *Grid) Set(x, y int, cell Cell) {
	if !g.InBounds(x, y) {
		return
	}
	g.Cells[y*g.Width+x] = cell
}

// Open reports whether (x, y) is in bounds and not BlocksMovement — the
// common "can a token stand here" check, used by both generation
// (connectivity checks) and movement validation.
func (g *Grid) Open(x, y int) bool {
	cell, ok := g.At(x, y)
	return ok && !cell.BlocksMovement
}
