// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package dungeon

import (
	"math/rand"
	"testing"
)

// arrivalPoint returns the cell a party first stands on when they reach
// level idx: the dungeon entrance for level 0, otherwise the stairs-up
// landing.
func arrivalPoint(t *testing.T, d *Dungeon, idx int) (int, int) {
	t.Helper()
	if idx == 0 {
		return d.EntranceX, d.EntranceY
	}
	for _, s := range d.Levels[idx].Stairs {
		if s.Dir == StairUp {
			return s.X, s.Y
		}
	}
	t.Fatalf("level %d has no stairs-up landing", idx)
	return 0, 0
}

func floodFill(l *Level, startX, startY int) map[[2]int]bool {
	seen := map[[2]int]bool{}
	if !l.Walkable(startX, startY) {
		return seen
	}
	queue := [][2]int{{startX, startY}}
	seen[[2]int{startX, startY}] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			n := [2]int{cur[0] + d[0], cur[1] + d[1]}
			if seen[n] || !l.Walkable(n[0], n[1]) {
				continue
			}
			seen[n] = true
			queue = append(queue, n)
		}
	}
	return seen
}

func TestGenerate_EveryWalkableTileReachableFromArrival(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		d := Generate(Options{
			LevelWidth: 60, LevelHeight: 45, MaxLevels: 4,
			Rand: rand.New(rand.NewSource(seed)),
		})
		if d == nil {
			t.Fatalf("seed %d: Generate() = nil", seed)
		}
		for idx, l := range d.Levels {
			ax, ay := arrivalPoint(t, d, idx)
			reachable := floodFill(l, ax, ay)

			total := 0
			for y := 0; y < l.Height; y++ {
				for x := 0; x < l.Width; x++ {
					if l.Walkable(x, y) {
						total++
					}
				}
			}
			if total == 0 {
				t.Errorf("seed %d level %d: no walkable tiles", seed, idx)
				continue
			}
			if len(reachable) != total {
				t.Errorf("seed %d level %d: %d of %d walkable tiles reachable from arrival %v",
					seed, idx, len(reachable), total, [2]int{ax, ay})
			}
		}
	}
}

func TestGenerate_StairsCreatedInMatchingPairs(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		d := Generate(Options{
			LevelWidth: 50, LevelHeight: 50, MaxLevels: 5,
			Rand: rand.New(rand.NewSource(seed)),
		})
		for idx, l := range d.Levels {
			for _, s := range l.Stairs {
				if !s.Dir.IsValid() {
					t.Errorf("seed %d: level %d stair has invalid direction", seed, idx)
				}
				if s.ToLevel < 0 || s.ToLevel >= len(d.Levels) {
					t.Fatalf("seed %d: level %d stair ToLevel %d out of range", seed, idx, s.ToLevel)
				}
				tile, _ := l.At(s.X, s.Y)
				wantTile := TileStairsDown
				wantBack := StairUp
				if s.Dir == StairUp {
					wantTile, wantBack = TileStairsUp, StairDown
				}
				if tile != wantTile {
					t.Errorf("seed %d: level %d stair at %v is tile %v, want %v", seed, idx, [2]int{s.X, s.Y}, tile, wantTile)
				}
				// The far level must carry the reciprocal stair back to here.
				found := false
				for _, back := range d.Levels[s.ToLevel].Stairs {
					if back.ToLevel == idx && back.Dir == wantBack {
						found = true
					}
				}
				if !found {
					t.Errorf("seed %d: level %d %v-stair to level %d has no reciprocal stair back", seed, idx, s.Dir, s.ToLevel)
				}
			}
		}
	}
}

func TestGenerate_SecretDoorsAreRealConnectionsThatReadAsWall(t *testing.T) {
	sawSecret := false
	for seed := int64(0); seed < 40 && !sawSecret; seed++ {
		d := Generate(Options{
			LevelWidth: 70, LevelHeight: 50, MaxLevels: 3,
			Rand: rand.New(rand.NewSource(seed)),
		})
		for idx, l := range d.Levels {
			for i, tile := range l.Tiles {
				if tile != TileSecretDoor {
					continue
				}
				sawSecret = true
				if !tile.Walkable() || !tile.BlocksLOS() {
					t.Errorf("seed %d level %d: secret door should be walkable and block LOS", seed, idx)
				}
				x, y := i%l.Width, i/l.Width
				// A secret door sits between an open cell and (from the
				// renderer's view) wall — it's a doorway, not an orphan.
				open := 0
				for _, dxy := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					if t, ok := l.At(x+dxy[0], y+dxy[1]); ok && t.Walkable() {
						open++
					}
				}
				if open == 0 {
					t.Errorf("seed %d level %d: secret door at (%d,%d) connects to nothing", seed, idx, x, y)
				}
			}
		}
	}
	if !sawSecret {
		t.Error("no secret door generated across 40 seeds — the feature never fires")
	}
	// Connectivity (TestGenerate_EveryWalkableTileReachableFromArrival)
	// already proves the dungeon stays fully connected *through* these,
	// since floodFill treats a secret door as walkable.
}

func TestGenerate_RespectsMaxLevels(t *testing.T) {
	for seed := int64(0); seed < 15; seed++ {
		d := Generate(Options{
			LevelWidth: 40, LevelHeight: 40, MaxLevels: 3,
			Rand: rand.New(rand.NewSource(seed)),
		})
		if len(d.Levels) > 3 {
			t.Errorf("seed %d: %d levels, want at most 3", seed, len(d.Levels))
		}
		if len(d.Levels) < 1 {
			t.Errorf("seed %d: %d levels, want at least the entrance", seed, len(d.Levels))
		}
	}
}

func TestGenerate_DeterministicForSameSeed(t *testing.T) {
	mk := func() *Dungeon {
		return Generate(Options{
			LevelWidth: 50, LevelHeight: 40, MaxLevels: 4, RoomSize: RoomSizeHuge,
			Rand: rand.New(rand.NewSource(99)),
		})
	}
	a, b := mk(), mk()
	if len(a.Levels) != len(b.Levels) {
		t.Fatalf("level count differs: %d vs %d", len(a.Levels), len(b.Levels))
	}
	for i := range a.Levels {
		la, lb := a.Levels[i], b.Levels[i]
		for j := range la.Tiles {
			if la.Tiles[j] != lb.Tiles[j] {
				t.Fatalf("level %d tile %d differs between runs: %v vs %v", i, j, la.Tiles[j], lb.Tiles[j])
			}
		}
		if len(la.Features) != len(lb.Features) || len(la.Stairs) != len(lb.Stairs) {
			t.Fatalf("level %d feature/stair counts differ", i)
		}
	}
}

func TestGenerate_StaysInBounds(t *testing.T) {
	d := Generate(Options{
		LevelWidth: 33, LevelHeight: 21, MaxLevels: 3,
		Rand: rand.New(rand.NewSource(7)),
	})
	for idx, l := range d.Levels {
		if l.Width != 33 || l.Height != 21 || len(l.Tiles) != 33*21 {
			t.Fatalf("level %d dims %dx%d, %d tiles", idx, l.Width, l.Height, len(l.Tiles))
		}
		for _, f := range l.Features {
			if !l.InBounds(f.X, f.Y) {
				t.Errorf("level %d feature at %v out of bounds", idx, [2]int{f.X, f.Y})
			}
			if !l.Walkable(f.X, f.Y) {
				t.Errorf("level %d feature at %v is not on a walkable tile", idx, [2]int{f.X, f.Y})
			}
		}
		for _, s := range l.Stairs {
			if !l.InBounds(s.X, s.Y) {
				t.Errorf("level %d stair at %v out of bounds", idx, [2]int{s.X, s.Y})
			}
		}
	}
}

func TestGenerate_RoomSizeBiasesChamberDimensions(t *testing.T) {
	meanDim := func(size RoomSize) float64 {
		g := &generator{opts: Options{RoomSize: size}, rnd: rand.New(rand.NewSource(1))}
		var sum, n int
		for i := 0; i < 4000; i++ {
			w, h := g.chamberDims()
			sum += w + h
			n += 2
		}
		return float64(sum) / float64(n)
	}
	cramped := meanDim(RoomSizeCramped)
	average := meanDim(RoomSizeAverage)
	huge := meanDim(RoomSizeHuge)
	if !(cramped < average && average < huge) {
		t.Errorf("mean chamber dimension should grow cramped < average < huge, got %.2f / %.2f / %.2f", cramped, average, huge)
	}
}

func TestGenerate_InvalidOptions_ReturnsNil(t *testing.T) {
	cases := map[string]Options{
		"zero width":  {LevelWidth: 0, LevelHeight: 10, MaxLevels: 1, Rand: rand.New(rand.NewSource(1))},
		"zero height": {LevelWidth: 10, LevelHeight: 0, MaxLevels: 1, Rand: rand.New(rand.NewSource(1))},
		"zero levels": {LevelWidth: 10, LevelHeight: 10, MaxLevels: 0, Rand: rand.New(rand.NewSource(1))},
		"nil rand":    {LevelWidth: 10, LevelHeight: 10, MaxLevels: 1, Rand: nil},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Generate(opts); got != nil {
				t.Errorf("Generate() = %+v, want nil", got)
			}
		})
	}
}

func TestGenerate_TinyBudget_StillTerminatesWithAConnectedEntrance(t *testing.T) {
	d := Generate(Options{
		LevelWidth: 40, LevelHeight: 40, MaxLevels: 3, GrowthBudget: 1,
		Rand: rand.New(rand.NewSource(3)),
	})
	if d == nil || len(d.Levels) == 0 {
		t.Fatal("Generate() with a tiny budget should still return the entrance level")
	}
	l0 := d.Levels[0]
	reachable := floodFill(l0, d.EntranceX, d.EntranceY)
	if len(reachable) < 9 {
		t.Errorf("entrance landing should be carved: only %d reachable tiles", len(reachable))
	}
}
