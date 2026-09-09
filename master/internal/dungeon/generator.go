// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package dungeon

import "math/rand"

// RoomSize biases how large the generator's chambers come out — the same
// three-way knob the older VB generator exposed.
type RoomSize uint8

const (
	// RoomSizeAverage is the default spread.
	RoomSizeAverage RoomSize = iota
	// RoomSizeCramped favors small chambers.
	RoomSizeCramped
	// RoomSizeHuge favors large ones.
	RoomSizeHuge
)

// Options controls Generate.
type Options struct {
	// LevelWidth, LevelHeight are every level's grid dimensions. Both
	// must be positive; small values just yield a small dungeon.
	LevelWidth, LevelHeight int
	// MaxLevels caps how many levels the dungeon can have (>=1). A stairs
	// result that would exceed it becomes a dead end instead.
	MaxLevels int
	// GrowthBudget bounds the total number of expansion points the
	// generator will process across all levels — the analog of the older
	// generator's component budget. The run also stops early when the
	// frontier empties. Non-positive falls back to a size-derived
	// default.
	GrowthBudget int
	// RoomSize biases chamber sizes.
	RoomSize RoomSize
	// Rand is the source of randomness. Required — a nil Rand returns
	// nil rather than falling back to a process-global source, so
	// "deterministic for tests" can't be lost by omission (same contract
	// as combatmap.GenerateOptions.Rand).
	Rand *rand.Rand
}

// direction is a 4-way orthogonal facing. dx/dy in level coordinates
// (y grows downward).
type direction uint8

const (
	north direction = iota
	east
	south
	west
)

func (d direction) delta() (int, int) {
	switch d {
	case north:
		return 0, -1
	case east:
		return 1, 0
	case south:
		return 0, 1
	default:
		return -1, 0
	}
}

func (d direction) left() direction   { return (d + 3) % 4 }
func (d direction) right() direction  { return (d + 1) % 4 }
func (d direction) around() direction { return (d + 2) % 4 }

// expansionPoint is a pending place to grow from: a walkable cell on a
// level, and the direction growth should head.
type expansionPoint struct {
	level int
	x, y  int
	dir   direction
}

// generator holds the mutable state of one Generate call.
type generator struct {
	opts     Options
	rnd      *rand.Rand
	dungeon  *Dungeon
	frontier []expansionPoint
	budget   int
}

// frontierSoftCap bounds how large the frontier is allowed to grow
// before the generator stops opening new chambers and staircases and
// only extends or closes existing passages — the same "stop sprawling
// once it's big enough" throttle the older generator applied by shrinking
// rooms past a threshold.
const frontierSoftCap = 120

// Generate builds a dungeon per opts. Returns nil for a malformed
// request (non-positive dimensions, MaxLevels < 1, or a nil Rand) — not
// a generation failure, since the frontier-driven algorithm always
// produces a connected result once it has valid inputs.
//
// Connectivity guarantee: every walkable tile on a level is reachable
// from that level's arrival point (the entrance on level 0, the
// stairs-up landing on any deeper level) by 4-way movement, because
// every carve extends from an already-walkable cell. Levels connect only
// through staircases, always created in matching up/down pairs.
func Generate(opts Options) *Dungeon {
	if opts.LevelWidth <= 0 || opts.LevelHeight <= 0 || opts.MaxLevels < 1 || opts.Rand == nil {
		return nil
	}

	budget := opts.GrowthBudget
	if budget <= 0 {
		budget = (opts.LevelWidth * opts.LevelHeight) / 25
		if budget < 8 {
			budget = 8
		}
	}

	g := &generator{
		opts:    opts,
		rnd:     opts.Rand,
		dungeon: &Dungeon{},
		budget:  budget,
	}

	entrance := g.newLevel(0)
	g.dungeon.Levels = append(g.dungeon.Levels, entrance)
	ex, ey := g.carveLanding(0, opts.LevelWidth/2, opts.LevelHeight/2)
	g.dungeon.EntranceX, g.dungeon.EntranceY = ex, ey

	for len(g.frontier) > 0 && g.budget > 0 {
		xp := g.frontier[0]
		g.frontier = g.frontier[1:]
		g.budget--
		g.grow(xp)
	}
	return g.dungeon
}

func (g *generator) newLevel(depth int) *Level {
	return &Level{
		Depth:  depth,
		Width:  g.opts.LevelWidth,
		Height: g.opts.LevelHeight,
		Tiles:  make([]TileType, g.opts.LevelWidth*g.opts.LevelHeight),
	}
}

// carveLanding opens a small room around (x, y) on a level and seeds an
// expansion point out of it in each cardinal direction that has room —
// used for both the entrance and every stairs-up arrival.
func (g *generator) carveLanding(level, x, y int) (int, int) {
	l := g.dungeon.Levels[level]
	// Keep the 3x3 landing (and its one-cell exits) inside the carvable
	// area even on a small level.
	x = clamp(x, 2, l.Width-3)
	y = clamp(y, 2, l.Height-3)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			l.set(x+dx, y+dy, TileFloor)
		}
	}
	for _, d := range []direction{north, east, south, west} {
		dx, dy := d.delta()
		if l.carvable(x+dx*2, y+dy*2) {
			g.frontier = append(g.frontier, expansionPoint{level: level, x: x + dx, y: y + dy, dir: d})
		}
	}
	return x, y
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// grow processes one expansion point: the periodic check.
func (g *generator) grow(xp expansionPoint) {
	l := g.dungeon.Levels[xp.level]
	if !l.InBounds(xp.x, xp.y) {
		return
	}
	dx, dy := xp.dir.delta()
	// Already opened straight ahead — this point is spent.
	if t, ok := l.At(xp.x+dx, xp.y+dy); ok && t.Walkable() {
		return
	}

	crowded := len(g.frontier) >= frontierSoftCap
	roll := g.rnd.Intn(100)
	switch {
	case roll < 55 || crowded && roll < 95:
		g.growPassage(xp)
	case roll < 60:
		// dead end — drop it
	case roll < 92 && !crowded:
		g.growChamber(xp)
	case !crowded:
		g.growStairs(xp)
	default:
		g.growPassage(xp)
	}
}

// passageLength rolls a corridor length in the spirit of the older
// generator's Rand(5)*6 — a handful of 6-cell segments.
func (g *generator) passageLength() int {
	return (1 + g.rnd.Intn(5)) * 6
}

// carveCorridor digs forward from (x, y) along dir for up to length
// cells, stopping early at the level edge or the first already-walkable
// cell (which connects this corridor to whatever is there). Returns the
// cell it stopped on and whether it moved at all.
func (g *generator) carveCorridor(level, x, y int, dir direction, length int) (int, int, bool) {
	l := g.dungeon.Levels[level]
	dx, dy := dir.delta()
	cx, cy := x, y
	moved := false
	for i := 0; i < length; i++ {
		nx, ny := cx+dx, cy+dy
		if !l.carvable(nx, ny) {
			break
		}
		if t, _ := l.At(nx, ny); t.Walkable() {
			cx, cy = nx, ny
			moved = true
			break
		}
		l.set(nx, ny, TileFloor)
		cx, cy = nx, ny
		moved = true
	}
	return cx, cy, moved
}

func (g *generator) growPassage(xp expansionPoint) {
	ex, ey, moved := g.carveCorridor(xp.level, xp.x, xp.y, xp.dir, g.passageLength())
	if !moved {
		return
	}
	switch r := g.rnd.Intn(100); {
	case r < 60:
		g.enqueue(xp.level, ex, ey, xp.dir)
	case r < 80:
		d := xp.dir.left()
		if g.rnd.Intn(2) == 0 {
			d = xp.dir.right()
		}
		g.enqueue(xp.level, ex, ey, d)
	case r < 92:
		// side passage plus continue straight
		g.enqueue(xp.level, ex, ey, xp.dir)
		side := xp.dir.left()
		if g.rnd.Intn(2) == 0 {
			side = xp.dir.right()
		}
		g.enqueue(xp.level, ex, ey, side)
	default:
		// T — both sides, no straight
		g.enqueue(xp.level, ex, ey, xp.dir.left())
		g.enqueue(xp.level, ex, ey, xp.dir.right())
	}
}

// chamberDims rolls a chamber's interior width and height (independently)
// per RoomSize. The three distributions are deliberately monotonic in
// their mean — cramped < average < huge — so the knob does what its name
// says: cramped ~2.8 cells/side, average ~4, huge ~5.5.
func (g *generator) chamberDims() (int, int) {
	d3 := func() int { return 1 + g.rnd.Intn(3) }
	roll := func() int {
		switch g.opts.RoomSize {
		case RoomSizeCramped:
			switch r := g.rnd.Intn(100); {
			case r < 65:
				return d3()
			case r < 90:
				return d3() + d3()
			default:
				return d3() + 3
			}
		case RoomSizeHuge:
			if g.rnd.Intn(2) == 0 {
				return d3() + d3()
			}
			return d3() + d3() + 3
		default: // Average
			return d3() + d3()
		}
	}
	return roll(), roll()
}

func (g *generator) growChamber(xp expansionPoint) {
	if g.tryChamber(xp) {
		return
	}
	// Nowhere to put it (edge or something already there) — don't lose
	// the frontier point, spend it as a passage instead, the same
	// fallback the older generator used.
	g.growPassage(xp)
}

// tryChamber attempts to place a chamber; returns false without touching
// the map if it doesn't fit.
func (g *generator) tryChamber(xp expansionPoint) bool {
	l := g.dungeon.Levels[xp.level]
	w, h := g.chamberDims()

	// Lay the chamber out ahead of the expansion point, spanning to the
	// sides with a random offset so the entry isn't always centered.
	dx, dy := xp.dir.delta()
	doorX, doorY := xp.x+dx, xp.y+dy // the doorway cell
	var minX, minY, maxX, maxY int
	if dx != 0 { // heading east or west
		aheadX := doorX + dx // chamber's near edge
		if dx > 0 {
			minX, maxX = aheadX, aheadX+w-1
		} else {
			minX, maxX = aheadX-w+1, aheadX
		}
		off := g.rnd.Intn(h)
		minY, maxY = doorY-off, doorY-off+h-1
	} else { // heading north or south
		aheadY := doorY + dy
		if dy > 0 {
			minY, maxY = aheadY, aheadY+h-1
		} else {
			minY, maxY = aheadY-h+1, aheadY
		}
		off := g.rnd.Intn(w)
		minX, maxX = doorX-off, doorX-off+w-1
	}

	// Reject if the chamber (with a 1-cell inspection margin) leaves the
	// carvable area or would touch anything already carved.
	for y := minY - 1; y <= maxY+1; y++ {
		for x := minX - 1; x <= maxX+1; x++ {
			if !l.carvable(x, y) {
				return false
			}
			if t, _ := l.At(x, y); t.Walkable() {
				return false
			}
		}
	}

	l.set(doorX, doorY, TileDoor)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			l.set(x, y, TileFloor)
		}
	}
	g.scatterFeatures(xp.level, minX, minY, maxX, maxY)
	g.chamberExits(xp.level, minX, minY, maxX, maxY, xp.dir)
	return true
}

// scatterFeatures drops a few features on interior chamber cells.
func (g *generator) scatterFeatures(level, minX, minY, maxX, maxY int) {
	if maxX-minX < 2 || maxY-minY < 2 {
		return
	}
	l := g.dungeon.Levels[level]
	placed := 0
	for y := minY + 1; y < maxY && placed < 3; y++ {
		for x := minX + 1; x < maxX && placed < 3; x++ {
			if g.rnd.Intn(100) >= 8 {
				continue
			}
			var kind FeatureKind
			switch g.rnd.Intn(4) {
			case 0:
				kind = FeatureFountain
			case 1:
				kind = FeatureStatue
			case 2:
				kind = FeatureShrine
			default:
				kind = FeatureChest
			}
			l.Features = append(l.Features, Feature{X: x, Y: y, Kind: kind})
			placed++
		}
	}
}

// chamberExits punches 1-3 doorways through the chamber walls (never the
// wall the entry door is on), carves a stub corridor out of each, and
// enqueues an expansion point there. Exits are spread across the
// remaining walls rather than clustered.
func (g *generator) chamberExits(level, minX, minY, maxX, maxY int, entryDir direction) {
	l := g.dungeon.Levels[level]
	entryWall := entryDir.around() // the chamber wall the entry door sits on

	type wall struct {
		dir direction
		try func() (x, y int)
	}
	walls := []wall{
		{north, func() (int, int) { return minX + g.rnd.Intn(maxX-minX+1), minY - 1 }},
		{south, func() (int, int) { return minX + g.rnd.Intn(maxX-minX+1), maxY + 1 }},
		{west, func() (int, int) { return minX - 1, minY + g.rnd.Intn(maxY-minY+1) }},
		{east, func() (int, int) { return maxX + 1, minY + g.rnd.Intn(maxY-minY+1) }},
	}
	g.rnd.Shuffle(len(walls), func(i, j int) { walls[i], walls[j] = walls[j], walls[i] })

	want := 1 + g.rnd.Intn(3)
	made := 0
	for _, wl := range walls {
		if made >= want {
			break
		}
		if wl.dir == entryWall {
			continue
		}
		for attempt := 0; attempt < 6; attempt++ {
			ox, oy := wl.try()
			if !l.carvable(ox, oy) {
				continue
			}
			if t, _ := l.At(ox, oy); t.Walkable() {
				continue
			}
			// Behind the doorway must be solid so we're not punching
			// straight back into this same chamber via a re-entrant wall.
			ddx, ddy := wl.dir.delta()
			if t, ok := l.At(ox+ddx, oy+ddy); !ok || t.Walkable() {
				continue
			}
			door := TileDoor
			// ~1 in 6 chamber exits is a secret door — the region beyond
			// it is still connected (Walkable), a party just has to find
			// it. Never the first exit, so a chamber is never sealed off
			// entirely behind secrets.
			if made > 0 && g.rnd.Intn(6) == 0 {
				door = TileSecretDoor
			}
			l.set(ox, oy, door)
			g.enqueue(level, ox, oy, wl.dir)
			made++
			break
		}
	}
}

func (g *generator) growStairs(xp expansionPoint) {
	if len(g.dungeon.Levels) >= g.opts.MaxLevels {
		return // dead end — no room to go deeper
	}
	l := g.dungeon.Levels[xp.level]
	// Step one cell forward so the stair isn't on the passage cell the
	// expansion point already sits on.
	dx, dy := xp.dir.delta()
	sx, sy := xp.x+dx, xp.y+dy
	if !l.carvable(sx, sy) {
		return
	}
	l.set(sx, sy, TileStairsDown)

	childIdx := len(g.dungeon.Levels)
	child := g.newLevel(l.Depth + 1)
	g.dungeon.Levels = append(g.dungeon.Levels, child)
	cx, cy := g.carveLanding(childIdx, child.Width/2, child.Height/2)
	child.set(cx, cy, TileStairsUp)

	l.Stairs = append(l.Stairs, Stair{X: sx, Y: sy, Dir: StairDown, ToLevel: childIdx})
	child.Stairs = append(child.Stairs, Stair{X: cx, Y: cy, Dir: StairUp, ToLevel: xp.level})
}

// enqueue adds an expansion point, guarding bounds and the frontier hard
// ceiling.
func (g *generator) enqueue(level, x, y int, dir direction) {
	if len(g.frontier) >= frontierSoftCap*3 {
		return
	}
	if !g.dungeon.Levels[level].carvable(x, y) {
		return
	}
	g.frontier = append(g.frontier, expansionPoint{level: level, x: x, y: y, dir: dir})
}
