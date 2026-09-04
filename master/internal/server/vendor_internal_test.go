// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import "testing"

func TestCopperToDenominations_VariousAmounts_DecomposesGreedily(t *testing.T) {
	tests := []struct {
		name                                           string
		totalCopper                                    int64
		wantCopper, wantSilver, wantGold, wantPlatinum int32
	}{
		{"Zero_AllZero", 0, 0, 0, 0, 0},
		{"OneCopper_AllInCopper", 1, 1, 0, 0, 0},
		{"ExactlyOneGold_NoRemainder", 100, 0, 0, 1, 0},
		{"ExactlyOnePlatinum_NoRemainder", 1000, 0, 0, 0, 1},
		{"MixedDenominations", 1234, 4, 3, 2, 1},
		{"FiftyCopper_DecomposesToFiveSilver", 50, 0, 5, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copper, silver, gold, platinum := copperToDenominations(tt.totalCopper)
			if copper != tt.wantCopper || silver != tt.wantSilver || gold != tt.wantGold || platinum != tt.wantPlatinum {
				t.Errorf("copperToDenominations(%d) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					tt.totalCopper, copper, silver, gold, platinum,
					tt.wantCopper, tt.wantSilver, tt.wantGold, tt.wantPlatinum)
			}
		})
	}
}

func TestDenominationsToCopper_IsCopperToDenominationsInverse(t *testing.T) {
	tests := []struct {
		name                           string
		copper, silver, gold, platinum int32
		wantTotal                      int64
	}{
		{"Zero", 0, 0, 0, 0, 0},
		{"AllDenominations", 4, 3, 2, 1, 1234},
		{"OnlyPlatinum", 0, 0, 0, 5, 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := denominationsToCopper(tt.copper, tt.silver, tt.gold, tt.platinum)
			if got != tt.wantTotal {
				t.Errorf("denominationsToCopper(%d,%d,%d,%d) = %d, want %d", tt.copper, tt.silver, tt.gold, tt.platinum, got, tt.wantTotal)
			}
		})
	}
}
