//go:build linux

package tray

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
)

// The Linux icons have to carry colour, and the colour has to be visible.
//
// This is the failure the platform makes easy. fyne.io/systray's SetTemplateIcon
// discards the template here and hands the panel the plain bytes, so embedding the
// black template macOS wants gives a black glyph on a dark panel: an icon that is
// working and invisible, which reads as an agent that is not running — the one thing
// this icon exists to disprove. Asserted on the bytes rather than on the generator,
// because what ships is the committed file.
func TestTheLinuxIconsAreVisibleOnAPanel(t *testing.T) {
	// Light and dark, at the extremes the common themes actually use: white is the
	// lightest panel there is, and #2E3436 is Adwaita's dark bar — Breeze's is darker
	// still, so clearing this one clears that.
	//
	// TODO: no single flat colour clears this bar against a panel of middling
	// luminance — a saturated blue, say. The upgrade is an outline in the opposite
	// luminance, and the grey the Windows icon ships has the same ceiling.
	const floor = 3.0 // WCAG 1.4.11, the threshold for a graphical object
	panels := map[string]color.NRGBA{
		"a white panel": {R: 0xFF, G: 0xFF, B: 0xFF},
		"a dark panel":  {R: 0x2E, G: 0x34, B: 0x36},
	}

	for name, raw := range map[string][]byte{
		"masking": maskingIcon, "partial": partialIcon,
		"unmasked": unmaskedIcon, "absent": absentIcon,
	} {
		ink := densestColour(t, raw)
		if ink.R == 0 && ink.G == 0 && ink.B == 0 {
			t.Errorf("%s is drawn in black, which is the macOS template: invisible on a dark panel", name)
			continue
		}
		for panel, against := range panels {
			if got := contrast(ink, against); got < floor {
				t.Errorf("%s is #%02X%02X%02X, which is %.2f:1 against %s, want %.1f:1",
					name, ink.R, ink.G, ink.B, got, panel, floor)
			}
		}
	}
}

// densestColour is the colour the glyph is drawn in: the most common one among the
// pixels the renderer left fully opaque.
//
// The edges are antialiased, so averaging every pixel that carries any alpha would
// report a colour no pixel actually holds and would drift towards whatever the
// coverage happened to be. The interior is one flat hue by construction.
func densestColour(t *testing.T, raw []byte) color.NRGBA {
	t.Helper()

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	counts := map[color.NRGBA]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a < 0xFFFF {
				continue
			}
			counts[color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 0xFF}]++
		}
	}

	var best color.NRGBA
	var most int
	for c, n := range counts {
		if n > most {
			best, most = c, n
		}
	}
	if most == 0 {
		t.Fatal("the icon has no opaque pixel, so nothing is drawn")
	}
	return best
}

// contrast is the WCAG ratio between two opaque colours.
func contrast(a, b color.NRGBA) float64 {
	l1, l2 := relativeLuminance(a), relativeLuminance(b)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func relativeLuminance(c color.NRGBA) float64 {
	channel := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}
