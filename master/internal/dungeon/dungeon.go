// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package dungeon generates a multi-level explorable dungeon: rooms and
// corridors grown outward from a frontier of expansion points, with
// staircases minting deeper levels. It is the algorithm behind a future
// Gold Box / SSI-style first-person grid-crawler viewport (design doc
// §6.2, §11 V3) — the protocol already carries what such a viewport
// needs (token position, facing, room-adjacency), and this is the
// generator side of that.
//
// Structurally it follows the periodic-check shape of the 1979 Dungeon
// Masters Guide's Appendix A (roll each step: passage / chamber / stairs
// / dead end; size the chamber; roll its exits) — a clean-room take with
// entirely original probabilities and result set, the same
// tone-inspired-not-reproduced line the rest of this repo draws. It is a
// direct descendant of an older VB implementation of the same idea.
//
// This package is deliberately standalone: nothing in Master consumes it
// yet (the crawler viewport itself is unbuilt), it does not touch the
// System Engine, and it has no protocol surface. It exists so the
// algorithm is real, tested, and ready to wire when the viewport work
// starts — the same "build the mechanism ahead, wire the feature later"
// staging combatmap used for fog of war. Sibling package combatmap
// generates a single tactical combat map; this generates a whole
// dungeon to walk around in.
package dungeon

import "strings"

// TileType is one cell's terrain. The zero value is TileRock —
// unexcavated stone, the state every cell starts in before the generator
// carves it.
type TileType uint8

const (
	// TileRock is unexcavated stone: not walkable, blocks sight.
	TileRock TileType = iota
	// TileFloor is an open, walkable passage or chamber floor.
	TileFloor
	// TileDoor is a walkable doorway between a passage and a chamber (or
	// two chambers). Modeled as its own type so a viewport can draw it
	// and a future door/lock system has something to attach to.
	TileDoor
	// TileStairsUp / TileStairsDown are walkable floor carrying a stair
	// connection to another level — see Level.Stairs for the link.
	TileStairsUp
	TileStairsDown
)

// IsValid reports whether t is a defined TileType.
func (t TileType) IsValid() bool {
	return t <= TileStairsDown
}

// Walkable reports whether a creature can stand on a cell of this type.
func (t TileType) Walkable() bool {
	switch t {
	case TileFloor, TileDoor, TileStairsUp, TileStairsDown:
		return true
	default:
		return false
	}
}

// BlocksLOS reports whether this tile stops line of sight through it.
func (t TileType) BlocksLOS() bool {
	return t == TileRock
}

// FeatureKind is a point of interest sitting on an otherwise ordinary
// floor tile — flavor the generator scatters through chambers, not
// terrain. The zero value FeatureNone means "no feature", so it never
// appears in a Level.Features list.
type FeatureKind uint8

const (
	FeatureNone FeatureKind = iota
	FeatureFountain
	FeatureStatue
	FeatureShrine
	FeatureChest
)

// IsValid reports whether k is a real feature (not FeatureNone or an
// out-of-range value).
func (k FeatureKind) IsValid() bool {
	return k >= FeatureFountain && k <= FeatureChest
}

// Feature is a FeatureKind at a position on a Level.
type Feature struct {
	X, Y int
	Kind FeatureKind
}

// StairDir is which way a staircase goes.
type StairDir uint8

const (
	StairDirUnspecified StairDir = iota
	StairUp
	StairDown
)

// IsValid reports whether d names a real direction.
func (d StairDir) IsValid() bool {
	return d == StairUp || d == StairDown
}

// Stair links a stair tile on one level to the level it leads to. Every
// StairDown on level N has a matching StairUp on level ToLevel, and vice
// versa — the generator always creates them in pairs.
type Stair struct {
	X, Y    int
	Dir     StairDir
	ToLevel int // index into Dungeon.Levels
}

// Level is one floor of a dungeon: a Width*Height grid of tiles
// (row-major, index = y*Width + x), the features scattered on it, and
// its stair connections to other levels.
type Level struct {
	// Depth is 0 for the entrance level and increases downward. Purely
	// informational — level identity is the index into Dungeon.Levels.
	Depth         int
	Width, Height int
	Tiles         []TileType
	Features      []Feature
	Stairs        []Stair
}

// InBounds reports whether (x, y) is a real cell of l.
func (l *Level) InBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < l.Width && y < l.Height
}

// carvable reports whether (x, y) is in bounds with at least a one-cell
// margin from every edge — the generator never carves the outermost
// ring, so every walkable tile has solid rock on the far side of the
// wall and no token ever stands with the void at its shoulder.
func (l *Level) carvable(x, y int) bool {
	return x >= 1 && y >= 1 && x < l.Width-1 && y < l.Height-1
}

// At returns the tile at (x, y) and whether that position is in bounds,
// matching Go's comma-ok convention rather than panicking.
func (l *Level) At(x, y int) (tile TileType, ok bool) {
	if !l.InBounds(x, y) {
		return TileRock, false
	}
	return l.Tiles[y*l.Width+x], true
}

// set writes a tile at (x, y); a no-op out of bounds, since the
// generator routinely computes positions near an edge.
func (l *Level) set(x, y int, t TileType) {
	if l.InBounds(x, y) {
		l.Tiles[y*l.Width+x] = t
	}
}

// Walkable reports whether (x, y) is in bounds and a tile a creature can
// stand on.
func (l *Level) Walkable(x, y int) bool {
	t, ok := l.At(x, y)
	return ok && t.Walkable()
}

// String renders the level as ASCII, one rune per tile — for tests and
// debugging, not a shipped viewport. Features overprint their floor
// tile.
func (l *Level) String() string {
	runes := map[TileType]rune{
		TileRock: '#', TileFloor: '.', TileDoor: '+',
		TileStairsUp: '<', TileStairsDown: '>',
	}
	featRunes := map[FeatureKind]rune{
		FeatureFountain: 'F', FeatureStatue: 'S', FeatureShrine: 'H', FeatureChest: 'C',
	}
	grid := make([][]rune, l.Height)
	for y := range grid {
		grid[y] = make([]rune, l.Width)
		for x := range grid[y] {
			grid[y][x] = runes[l.Tiles[y*l.Width+x]]
		}
	}
	for _, f := range l.Features {
		if l.InBounds(f.X, f.Y) {
			grid[f.Y][f.X] = featRunes[f.Kind]
		}
	}
	var b strings.Builder
	for _, row := range grid {
		b.WriteString(string(row))
		b.WriteByte('\n')
	}
	return b.String()
}

// Dungeon is a generated multi-level dungeon. Levels[0] is the entrance
// level; deeper levels are reached only through staircases.
type Dungeon struct {
	Levels []*Level
	// EntranceX, EntranceY is the arrival point on Levels[0].
	EntranceX, EntranceY int
}
