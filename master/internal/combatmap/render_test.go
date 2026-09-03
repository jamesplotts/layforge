// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func decodeRenderedPNG(t *testing.T, dataURL string) (width, height int) {
	t.Helper()
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("dataURL = %q, want it to start with %q", dataURL, prefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatalf("base64 decode error = %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy()
}

func TestRender_ProducesCorrectlySizedPNG(t *testing.T) {
	g := NewGrid(4, 3)

	dataURL, err := Render(g, Visible(g, 0, 0, 0), nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	width, height := decodeRenderedPNG(t, dataURL)
	if width != 4*cellPixels || height != 3*cellPixels {
		t.Errorf("decoded image size = %dx%d, want %dx%d", width, height, 4*cellPixels, 3*cellPixels)
	}
}

func TestRender_UnseenCellsRenderAsFogNotFloorColor(t *testing.T) {
	g := NewGrid(3, 3)
	// Only (0, 0) is visible; everything else must render as fog.
	visible := map[Position]bool{{0, 0}: true}

	dataURL, err := Render(g, visible, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("base64 decode error = %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}

	// Center of the visible cell (0,0) should be floor-colored.
	visiblePixel := img.At(cellPixels/2, cellPixels/2)
	if !colorsEqual(visiblePixel, colorFloor) {
		t.Errorf("visible floor cell center color = %v, want %v", visiblePixel, colorFloor)
	}

	// Center of an unseen cell (2,2) should be fog-colored.
	unseenPixel := img.At(2*cellPixels+cellPixels/2, 2*cellPixels+cellPixels/2)
	if !colorsEqual(unseenPixel, colorFog) {
		t.Errorf("unseen cell center color = %v, want fog %v", unseenPixel, colorFog)
	}
}

func TestRender_TokenOutsideVisibleSet_NotDrawn(t *testing.T) {
	g := NewGrid(3, 3)
	visible := map[Position]bool{{0, 0}: true} // (2,2) NOT visible

	tokenColor := color.RGBA{255, 0, 0, 255}
	dataURL, err := Render(g, visible, []RenderToken{{X: 2, Y: 2, Color: tokenColor}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
	img, _ := png.Decode(bytes.NewReader(raw))

	center := img.At(2*cellPixels+cellPixels/2, 2*cellPixels+cellPixels/2)
	if colorsEqual(center, tokenColor) {
		t.Error("token drawn at a position outside the visible set, want it omitted")
	}
}

func TestRender_TokenInsideVisibleSet_Drawn(t *testing.T) {
	g := NewGrid(3, 3)
	visible := Visible(g, 1, 1, 0)

	tokenColor := color.RGBA{255, 0, 0, 255}
	dataURL, err := Render(g, visible, []RenderToken{{X: 1, Y: 1, Color: tokenColor}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
	img, _ := png.Decode(bytes.NewReader(raw))

	center := img.At(1*cellPixels+cellPixels/2, 1*cellPixels+cellPixels/2)
	if !colorsEqual(center, tokenColor) {
		t.Errorf("token center color = %v, want %v", center, tokenColor)
	}
}

func colorsEqual(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
