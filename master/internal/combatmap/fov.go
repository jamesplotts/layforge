// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

// Position is a single grid cell coordinate — used as a map key for a
// visible-cell set (Visible) and elsewhere in this package wherever a
// bare (x, y) pair needs a comparable, hashable type.
type Position struct {
	X, Y int
}

// Visible computes the set of cells visible from (originX, originY) via
// recursive symmetric shadowcasting against g's BlocksLOS cells — the
// standard algorithm documented on RogueBasin ("FOV using recursive
// shadowcasting"), same family of algorithm the (archived, MIT-compatible)
// gruid/rl Go package ships; this is a small hand-port rather than a
// vendored dependency, consistent with Master's single-static-binary, no
// runtime dependency design rule. The origin cell itself is always
// included, regardless of what it is. radius <= 0 means unbounded (limited
// only by g's own extent, not by distance) — a closed room has no reason
// to cap vision short of its walls.
//
// This is the whole of Master's own fog-of-war computation: it has nothing
// to do with, and never calls into, OpenCombatEngine's own
// IGridManager.HasLineOfSight — that's a separate, System-Engine-side
// concept for mechanical rulings (see this package's own doc comment for
// why the two are deliberately not wired together yet).
func Visible(g *Grid, originX, originY, radius int) map[Position]bool {
	visible := map[Position]bool{{originX, originY}: true}
	if g == nil || !g.InBounds(originX, originY) {
		return visible
	}

	maxRadius := radius
	if maxRadius <= 0 {
		maxRadius = g.Width + g.Height // always covers the whole grid
	}

	for _, t := range octantTransforms {
		castLight(g, visible, originX, originY, maxRadius, 1, 1.0, 0.0, t)
	}
	return visible
}

// transform maps a shadowcasting octant's local (deltaX, deltaY)
// coordinates onto real grid offsets from the origin — one of the eight
// symmetric reflections/rotations of the same algorithm.
type transform struct{ xx, xy, yx, yy int }

var octantTransforms = [8]transform{
	{1, 0, 0, 1}, {0, 1, 1, 0},
	{0, -1, 1, 0}, {-1, 0, 0, 1},
	{-1, 0, 0, -1}, {0, -1, -1, 0},
	{0, 1, -1, 0}, {1, 0, 0, -1},
}

func castLight(g *Grid, visible map[Position]bool, cx, cy, radius, row int, start, end float64, t transform) {
	if start < end {
		return
	}

	blocked := false
	for distance := row; distance <= radius && !blocked; distance++ {
		deltaY := -distance
		newStart := 0.0

		for deltaX := -distance; deltaX <= 0; deltaX++ {
			mapX := cx + deltaX*t.xx + deltaY*t.xy
			mapY := cy + deltaX*t.yx + deltaY*t.yy
			leftSlope := (float64(deltaX) - 0.5) / (float64(deltaY) + 0.5)
			rightSlope := (float64(deltaX) + 0.5) / (float64(deltaY) - 0.5)

			if !g.InBounds(mapX, mapY) || start < rightSlope {
				continue
			} else if end > leftSlope {
				break
			}

			if deltaX*deltaX+deltaY*deltaY < radius*radius {
				visible[Position{mapX, mapY}] = true
			}

			if blocked {
				if isOpaque(g, mapX, mapY) {
					newStart = rightSlope
					continue
				}
				blocked = false
				start = newStart
			} else if isOpaque(g, mapX, mapY) && distance < radius {
				blocked = true
				castLight(g, visible, cx, cy, radius, distance+1, start, leftSlope, t)
				newStart = rightSlope
			}
		}
	}
}

func isOpaque(g *Grid, x, y int) bool {
	cell, ok := g.At(x, y)
	return !ok || cell.BlocksLOS
}
