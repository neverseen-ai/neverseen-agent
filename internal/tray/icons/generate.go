//go:build ignore

// Generates the menu bar icons. Run it with:
//
//	go run ./internal/tray/icons/generate.go
//
// The result is committed, like testdata/heartbeats.json is: a generated asset a
// reviewer can look at beats a build step nobody can, and it keeps the agent free
// of an SVG rasteriser it would use exactly twice.
//
// # Why the geometry is restated here rather than read from the website's SVG
//
// The mark lives in cloakfleet-website/public/favicon.svg, and this draws the same
// shapes at the same proportions — but it is deliberately not the same image. A
// favicon sits on its own dark plate; a menu bar icon has no plate, is monochrome,
// and is handed to macOS as a template so the system recolours it for a light or a
// dark bar. Parsing the SVG would import a rasteriser to arrive at a different
// picture. If the mark itself ever changes, the numbers below are what has to
// follow it.
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// The drawing is in the mark's own 24-unit space, so the numbers below can be read
// against the SVG line by line, and the box below is cropped to the glyph rather
// than to the mark.
//
// Cropping matters more than it sounds. The favicon's divider runs the full height
// of its plate, which is right when a plate frames it; rendered as a menu bar icon
// the same proportions put a long thin line through a box whose middle third holds
// the only part that carries meaning — the two squares, one filled and one
// outlined. So the divider is shortened to the squares it separates and the box is
// pulled in around them, which is what makes them legible at sixteen points.
const (
	boxLeft   = 4.0
	boxRight  = 20.8
	boxTop    = 6.0
	boxBottom = 18.0

	// Thirty-two pixels because the darwin backend sets the image to 16 points and
	// macOS is a @2x world — no more is used, and less is soft on every Mac sold
	// in the last decade.
	pixels = 32

	// Each pixel is averaged over this many samples per side. Four is enough for
	// edges this size; the shapes are round rectangles and two lines, and the
	// alternative is a dependency to draw them.
	samples = 4
)

func main() {
	for name, state := range map[string]state{
		"masking.png":  stateMasking,
		"partial.png":  statePartial,
		"unmasked.png": stateUnmasked,
	} {
		if err := write(name, glyph(state)); err != nil {
			log.Fatal(err)
		}
	}
}

// state is how much of the catalogue is being applied, in the three answers the
// agent gives.
type state int

const (
	// stateMasking is every category the configuration loaded.
	stateMasking state = iota
	// statePartial is masking with categories switched off.
	statePartial
	// stateUnmasked is not masking at all: stopped, or no locale loaded.
	stateUnmasked
)

// glyph renders the mark in one of the three states.
//
// The state is carried by the mark's own vocabulary rather than by a badge over it:
// the left square is a value in clear, the right one is that value replaced. So the
// right square outlined means "being replaced", filled means "not", and half filled
// means "some of it is" — each one says exactly and only what is true.
//
// A strike through the mark was the obvious alternative and it was tried. At the
// sixteen points this is actually seen it is eight pixels of diagonal over a glyph
// it has to avoid notching, and whether it reads as "off" or as a smudge cannot be
// settled anywhere but a real menu bar. Filled against outlined is the strongest
// contrast available at that size and needs no sub-pixel luck.
//
// # Why there are three now, when the reasoning here said two
//
// It said two because what mattered was whether values were being replaced, and an
// agent up with no locale selected belonged with one that was down: both mean the
// traffic leaves in clear. That reasoning still holds and those two are still one
// picture.
//
// What changed is that a category can now be switched off from the menu. Such an
// agent is masking — most of the catalogue, and the credentials always — while the
// thing somebody switched off goes out in clear. Neither existing icon can say that:
// the masking one is the green light over the values that are not being replaced,
// and the unmasked one is a lie about the twenty-odd categories that are. The
// half-filled square is the third answer, and it exists for the same reason the
// status route distinguishes answering from masking.
func glyph(s state) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, pixels, pixels))

	// One scale for both axes, so the glyph is not stretched, and a margin of a
	// pixel so nothing is clipped by the rounding at the edges.
	const margin = 1.0
	scale := math.Min(
		(pixels-2*margin)/(boxRight-boxLeft),
		(pixels-2*margin)/(boxBottom-boxTop),
	)
	// Centred in the square the menu bar asks for.
	originX := (boxLeft+boxRight)/2 - pixels/2/scale
	originY := (boxTop+boxBottom)/2 - pixels/2/scale
	step := 1 / scale / samples

	for py := range pixels {
		for px := range pixels {
			covered := 0
			for sy := range samples {
				for sx := range samples {
					// The centre of this sub-sample, in the mark's own units.
					x := originX + (float64(px)*samples+float64(sx)+0.5)*step
					y := originY + (float64(py)*samples+float64(sy)+0.5)*step
					if inked(x, y, s) {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			// Black with coverage in the alpha: a template image is a shape, and
			// macOS paints it whatever colour the bar needs.
			alpha := uint8(covered * 255 / (samples * samples))
			out.SetNRGBA(px, py, color.NRGBA{A: alpha})
		}
	}
	return out
}

// inked reports whether a point in the mark's space is part of the glyph.
func inked(x, y float64, s state) bool {
	// The divider, shortened to the squares it separates: the SVG runs it from 4.5
	// to 19.5 because it has a plate to span.
	if toSegment(x, y, 12, 7.25, 12, 16.75) <= 1.5/2 {
		return true
	}
	// The value in clear, filled: x=4.75 y=9.25 5.5×5.5 rx=1.25.
	if roundedRect(x, y, 4.75, 9.25, 5.5, 5.5, 1.25) <= 0 {
		return true
	}

	// The same square on the right: outlined at stroke-width 1.6 while that value
	// is being replaced, filled when it is not, and half filled when some of the
	// catalogue is switched off.
	const rightLeft, rightWidth = 13.75, 5.5
	right := roundedRect(x, y, rightLeft, 9.25, rightWidth, 5.5, 1.25)
	outline := math.Abs(right) <= 1.6/2

	switch s {
	case stateMasking:
		return outline
	case statePartial:
		// The outline, plus the left half of what it encloses. Half rather than a
		// smaller inner square: at this size an inner shape is three pixels with a
		// one-pixel gap, which is a blur, while a straight edge down the middle of
		// a square survives being drawn at sixteen points on any Mac.
		return outline || (right <= 0 && x <= rightLeft+rightWidth/2)
	default:
		return right <= 1.6/2
	}
}

// roundedRect is the signed distance from a point to a rounded rectangle:
// negative inside, positive outside, zero on the edge.
func roundedRect(x, y, left, top, w, h, r float64) float64 {
	// Distance to the inner rectangle the corner radii are centred on, which is
	// what makes the corners round for free.
	dx := math.Max(math.Abs(x-(left+w/2))-(w/2-r), 0)
	dy := math.Max(math.Abs(y-(top+h/2))-(h/2-r), 0)
	outside := math.Hypot(dx, dy) - r

	// Inside, the distance is to the nearest edge — the largest of the two
	// one-dimensional distances, which are both negative there.
	inside := math.Max(
		math.Abs(x-(left+w/2))-w/2,
		math.Abs(y-(top+h/2))-h/2,
	)
	if inside < 0 {
		return math.Max(inside, -r)
	}
	return outside
}

// toSegment is the distance from a point to a line segment, which with a
// half-width test gives a stroke with round caps.
func toSegment(x, y, x1, y1, x2, y2 float64) float64 {
	vx, vy := x2-x1, y2-y1
	length := vx*vx + vy*vy
	if length == 0 {
		return math.Hypot(x-x1, y-y1)
	}
	t := math.Min(math.Max(((x-x1)*vx+(y-y1)*vy)/length, 0), 1)
	return math.Hypot(x-(x1+t*vx), y-(y1+t*vy))
}

func write(name string, img image.Image) error {
	path := filepath.Join("internal", "tray", "icons", name)
	file, err := os.Create(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if err := png.Encode(file, img); err != nil {
		return err
	}
	log.Printf("wrote %s", path)
	return nil
}
