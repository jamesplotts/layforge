// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import (
	"math/rand"
	"testing"
)

// floodFillOpenCells returns every open (non-BlocksMovement) cell
// reachable from (startX, startY) via 4-way adjacency — used to assert
// generator connectivity, the real correctness property for a randomized
// generator (exact tile layout isn't, and shouldn't be, asserted).
func floodFillOpenCells(t *testing.T, g *Grid, startX, startY int) map[Position]bool {
	t.Helper()
	visited := map[Position]bool{}
	if !g.Open(startX, startY) {
		return visited
	}
	queue := []Position{{startX, startY}}
	visited[Position{startX, startY}] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			next := Position{cur.X + d[0], cur.Y + d[1]}
			if visited[next] || !g.Open(next.X, next.Y) {
				continue
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	return visited
}

func TestGenerateRoomsAndCorridors_EveryOpenCellIsReachableFromEveryOther(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		g := GenerateRoomsAndCorridors(GenerateOptions{
			Width: 40, Height: 30, RoomAttempts: 25,
			MinRoomSize: 3, MaxRoomSize: 8,
			Rand: rand.New(rand.NewSource(seed)),
		})
		if g == nil {
			t.Fatalf("seed %d: GenerateRoomsAndCorridors() = nil", seed)
		}

		var totalOpen int
		var firstOpen Position
		foundFirst := false
		for y := 0; y < g.Height; y++ {
			for x := 0; x < g.Width; x++ {
				if g.Open(x, y) {
					totalOpen++
					if !foundFirst {
						firstOpen = Position{x, y}
						foundFirst = true
					}
				}
			}
		}
		if !foundFirst {
			t.Fatalf("seed %d: generated grid has no open cells at all", seed)
		}

		reachable := floodFillOpenCells(t, g, firstOpen.X, firstOpen.Y)
		if len(reachable) != totalOpen {
			t.Errorf("seed %d: %d of %d open cells reachable from %v, want all of them connected",
				seed, len(reachable), totalOpen, firstOpen)
		}
	}
}

func TestGenerateRoomsAndCorridors_StaysInBounds(t *testing.T) {
	g := GenerateRoomsAndCorridors(GenerateOptions{
		Width: 20, Height: 15, RoomAttempts: 30,
		MinRoomSize: 2, MaxRoomSize: 6,
		Rand: rand.New(rand.NewSource(1)),
	})

	if g.Width != 20 || g.Height != 15 {
		t.Fatalf("Grid dims = %dx%d, want 20x15", g.Width, g.Height)
	}
	if len(g.Cells) != 20*15 {
		t.Fatalf("len(Cells) = %d, want %d", len(g.Cells), 20*15)
	}
}

func TestGenerateRoomsAndCorridors_DeterministicForSameSeed(t *testing.T) {
	opts := GenerateOptions{
		Width: 25, Height: 20, RoomAttempts: 20,
		MinRoomSize: 3, MaxRoomSize: 7,
	}
	opts.Rand = rand.New(rand.NewSource(42))
	first := GenerateRoomsAndCorridors(opts)

	opts.Rand = rand.New(rand.NewSource(42))
	second := GenerateRoomsAndCorridors(opts)

	for i := range first.Cells {
		if first.Cells[i] != second.Cells[i] {
			t.Fatalf("cell %d differs between two runs with the same seed: %+v vs %+v", i, first.Cells[i], second.Cells[i])
		}
	}
}

func TestGenerateRoomsAndCorridors_InvalidOptions_ReturnsNil(t *testing.T) {
	tests := []struct {
		name string
		opts GenerateOptions
	}{
		{"zero_width", GenerateOptions{Width: 0, Height: 10, RoomAttempts: 5, MaxRoomSize: 3, Rand: rand.New(rand.NewSource(1))}},
		{"zero_height", GenerateOptions{Width: 10, Height: 0, RoomAttempts: 5, MaxRoomSize: 3, Rand: rand.New(rand.NewSource(1))}},
		{"zero_room_attempts", GenerateOptions{Width: 10, Height: 10, RoomAttempts: 0, MaxRoomSize: 3, Rand: rand.New(rand.NewSource(1))}},
		{"zero_max_room_size", GenerateOptions{Width: 10, Height: 10, RoomAttempts: 5, MaxRoomSize: 0, Rand: rand.New(rand.NewSource(1))}},
		{"nil_rand", GenerateOptions{Width: 10, Height: 10, RoomAttempts: 5, MaxRoomSize: 3, Rand: nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateRoomsAndCorridors(tt.opts); got != nil {
				t.Errorf("GenerateRoomsAndCorridors() = %+v, want nil", got)
			}
		})
	}
}
