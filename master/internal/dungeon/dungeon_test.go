// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package dungeon

import (
	"math/rand"
	"strings"
	"testing"
)

func TestTileType_WalkableAndLOS(t *testing.T) {
	cases := []struct {
		tile     TileType
		walkable bool
		blocks   bool
	}{
		{TileRock, false, true},
		{TileFloor, true, false},
		{TileDoor, true, false},
		{TileStairsUp, true, false},
		{TileStairsDown, true, false},
	}
	for _, c := range cases {
		if got := c.tile.Walkable(); got != c.walkable {
			t.Errorf("%v.Walkable() = %v, want %v", c.tile, got, c.walkable)
		}
		if got := c.tile.BlocksLOS(); got != c.blocks {
			t.Errorf("%v.BlocksLOS() = %v, want %v", c.tile, got, c.blocks)
		}
		if !c.tile.IsValid() {
			t.Errorf("%v.IsValid() = false", c.tile)
		}
	}
	if TileType(99).IsValid() {
		t.Error("TileType(99).IsValid() = true, want false")
	}
}

func TestEnumIsValid(t *testing.T) {
	if FeatureNone.IsValid() {
		t.Error("FeatureNone.IsValid() should be false")
	}
	if !FeatureChest.IsValid() || !FeatureFountain.IsValid() {
		t.Error("real FeatureKinds should be valid")
	}
	if FeatureKind(200).IsValid() {
		t.Error("out-of-range FeatureKind should be invalid")
	}
	if StairDirUnspecified.IsValid() {
		t.Error("StairDirUnspecified.IsValid() should be false")
	}
	if !StairUp.IsValid() || !StairDown.IsValid() {
		t.Error("StairUp/StairDown should be valid")
	}
}

func TestLevel_AtBounds(t *testing.T) {
	l := &Level{Width: 3, Height: 2, Tiles: make([]TileType, 6)}
	l.set(1, 1, TileFloor)
	if tile, ok := l.At(1, 1); !ok || tile != TileFloor {
		t.Errorf("At(1,1) = %v, %v; want floor, true", tile, ok)
	}
	if _, ok := l.At(-1, 0); ok {
		t.Error("At(-1,0) ok = true, want false")
	}
	if _, ok := l.At(3, 0); ok {
		t.Error("At(3,0) ok = true, want false")
	}
	l.set(99, 99, TileFloor) // out of bounds: must not panic
}

func TestLevel_StringRendersEveryRow(t *testing.T) {
	d := Generate(Options{
		LevelWidth: 20, LevelHeight: 8, MaxLevels: 1,
		Rand: rand.New(rand.NewSource(1)),
	})
	out := d.Levels[0].String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("String() produced %d rows, want 8", len(lines))
	}
	for i, line := range lines {
		if len(line) != 20 {
			t.Errorf("row %d is %d runes wide, want 20", i, len(line))
		}
	}
	if !strings.ContainsRune(out, '.') {
		t.Errorf("rendered level has no floor:\n%s", out)
	}
}
