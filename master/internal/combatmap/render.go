// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package combatmap

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// cellPixels is the rendered size, in pixels, of one grid cell — large
// enough to read as a sidebar thumbnail, small enough that a modest combat
// map stays a reasonable image size.
const cellPixels = 24

var (
	colorFog       = color.RGBA{20, 20, 24, 255}
	colorFloor     = color.RGBA{210, 200, 180, 255}
	colorWall      = color.RGBA{60, 55, 50, 255}
	colorDifficult = color.RGBA{150, 120, 70, 255}
	colorGridLine  = color.RGBA{170, 160, 140, 255}
)

// RenderToken is one creature to draw — deliberately just a position and a
// color, not a character/name/team: this package knows nothing about
// characters (see the package doc comment), the caller (internal/server)
// decides what color represents what. There is no token-art/image support
// yet — a plain filled circle stands in for a real token image, and there
// is no text label (a bitmap font is a real added dependency this package
// deliberately avoids for a first version) — the token_id/character_id in
// the accompanying protocol payload is what actually identifies a token to
// a client, the pixel itself doesn't need to.
type RenderToken struct {
	X, Y  int
	Color color.RGBA
}

// Render composites g into a PNG, encoded as a data: URL, showing only the
// cells in visible (fog of war already applied — everything else renders
// as unbroken fog, not merely dimmed) and only the tokens whose position
// is in visible. Returns an error only if PNG encoding itself fails (an
// image.Image invariant violation, not something a caller can usefully
// recover from — same "this shouldn't happen" class of error as
// json.Marshal failing on a struct with no unmarshalable fields).
func Render(g *Grid, visible map[Position]bool, tokens []RenderToken) (dataURL string, err error) {
	width := g.Width * cellPixels
	height := g.Height * cellPixels
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{colorFog}, image.Point{}, draw.Src)

	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			if !visible[Position{x, y}] {
				continue
			}
			cell, _ := g.At(x, y)
			fillCell(img, x, y, cellColor(cell))
		}
	}

	for _, tok := range tokens {
		if !visible[Position{tok.X, tok.Y}] {
			continue
		}
		fillTokenCircle(img, tok.X, tok.Y, tok.Color)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encoding combat map PNG: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func cellColor(cell Cell) color.RGBA {
	switch {
	case cell.BlocksMovement:
		return colorWall
	case cell.DifficultTerrain:
		return colorDifficult
	default:
		return colorFloor
	}
}

func fillCell(img *image.RGBA, cellX, cellY int, fill color.RGBA) {
	x0, y0 := cellX*cellPixels, cellY*cellPixels
	rect := image.Rect(x0, y0, x0+cellPixels, y0+cellPixels)
	draw.Draw(img, rect, &image.Uniform{fill}, image.Point{}, draw.Src)

	// A thin grid line on the cell's top/left edge — cheap visual
	// separation between cells without needing a real line-drawing
	// routine (the next cell's own top/left edge covers this cell's
	// bottom/right, so every internal boundary still gets a line).
	for x := x0; x < x0+cellPixels; x++ {
		img.Set(x, y0, colorGridLine)
	}
	for y := y0; y < y0+cellPixels; y++ {
		img.Set(x0, y, colorGridLine)
	}
}

// fillTokenCircle draws a filled circle centered in the given cell,
// leaving a visible border of the cell's own color around it so a token
// reads clearly against the grid.
func fillTokenCircle(img *image.RGBA, cellX, cellY int, fill color.RGBA) {
	cx := cellX*cellPixels + cellPixels/2
	cy := cellY*cellPixels + cellPixels/2
	radius := cellPixels/2 - 3
	radiusSq := radius * radius

	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radiusSq {
				img.Set(cx+dx, cy+dy, fill)
			}
		}
	}
}
