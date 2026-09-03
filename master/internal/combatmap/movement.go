// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

// FeetPerCell is the SRD's standard 5-foot grid square, used to convert a
// character's combatStats.speed (feet) into a cell budget for
// ValidateMove.
const FeetPerCell = 5

// ValidateMove reports whether a token can move from (fromX, fromY) to
// (toX, toY) on g within speedFeet of movement, and if so, the number of
// cells that move actually costs. It rejects (ok=false) a destination
// that's out of bounds, blocked, or unreachable from the origin without
// crossing a blocked cell — found via a breadth-first search over 8-way
// adjacency (diagonal movement costs the same as orthogonal, one cell per
// step; this doesn't implement the SRD's optional "every other diagonal
// costs double" variant rule, a deliberate simplification for a first
// version, not an oversight) bounded by speedFeet/FeetPerCell steps, so a
// destination that exists but is simply too far never even gets searched.
//
// Ownership (is the requester allowed to move this particular token at
// all) is not this function's concern — that's checked by the caller
// (internal/server's map.token_move_request handler) before this is ever
// called, the same "gate at the handler, validate the move mechanically
// here" split CastSpellAction/dmCastSpell already use for spellcasting.
func ValidateMove(g *Grid, fromX, fromY, toX, toY, speedFeet int) (ok bool, cellsCost int) {
	if g == nil || !g.InBounds(fromX, fromY) || !g.Open(toX, toY) {
		return false, 0
	}
	if fromX == toX && fromY == toY {
		return true, 0
	}

	maxSteps := speedFeet / FeetPerCell
	if maxSteps <= 0 {
		return false, 0
	}

	type node struct{ x, y int }
	start := node{fromX, fromY}
	target := node{toX, toY}

	dist := map[node]int{start: 0}
	queue := []node{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curDist := dist[cur]
		if cur == target {
			return true, curDist
		}
		if curDist >= maxSteps {
			continue
		}
		for _, d := range neighborOffsets {
			next := node{cur.x + d.dx, cur.y + d.dy}
			if !g.Open(next.x, next.y) {
				continue
			}
			if _, seen := dist[next]; seen {
				continue
			}
			dist[next] = curDist + 1
			queue = append(queue, next)
		}
	}
	return false, 0
}

var neighborOffsets = []struct{ dx, dy int }{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
}
