// Package icon generates the tray icon at runtime so the project carries no
// binary assets. It renders a small two-tone glyph as PNG; platform wrappers
// (see icon_*.go) adapt it to the format the tray expects on each OS.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

const size = 32

// baseImage renders the icon: a rounded-ish filled square split into two tones,
// evoking "two accounts".
func baseImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	left := color.RGBA{0xD9, 0x77, 0x57, 0xFF}  // Claude clay/orange
	right := color.RGBA{0x2B, 0x2A, 0x28, 0xFF} // near-black
	clear := color.RGBA{0, 0, 0, 0}

	const r = 6 // corner radius
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !inRoundedRect(x, y, size, size, r) {
				img.Set(x, y, clear)
				continue
			}
			if x < size/2 {
				img.Set(x, y, left)
			} else {
				img.Set(x, y, right)
			}
		}
	}
	return img
}

// pngBytes returns the encoded base icon.
func pngBytes() []byte { return encode(baseImage()) }

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// SpinnerFrames returns PNG frames for an in-progress animation: a bright dot
// orbiting the icon. Cycle through them to show the user a switch is running.
func SpinnerFrames() [][]byte {
	const n = 8
	accent := color.RGBA{0xFF, 0xC8, 0x8A, 0xFF} // light clay highlight
	frames := make([][]byte, n)
	for f := 0; f < n; f++ {
		img := baseImage()
		ang := 2 * math.Pi * float64(f) / float64(n)
		cx := float64(size)/2 + math.Cos(ang)*float64(size)*0.3
		cy := float64(size)/2 + math.Sin(ang)*float64(size)*0.3
		drawDot(img, int(cx), int(cy), 6, accent)
		frames[f] = encode(img)
	}
	return frames
}

// swatchPalette cycles distinct, readable tones for MultiProfileIcon so
// adjacent profiles stay visually distinguishable regardless of count.
var swatchPalette = []color.RGBA{
	{0xD9, 0x77, 0x57, 0xFF}, // Claude clay/orange
	{0x2B, 0x2A, 0x28, 0xFF}, // near-black
	{0x5B, 0x8A, 0x72, 0xFF}, // sage
	{0x6B, 0x7C, 0xC4, 0xFF}, // periwinkle
	{0xC4, 0x8A, 0x3E, 0xFF}, // amber
	{0x9A, 0x5B, 0x8A, 0xFF}, // plum
}

// PaletteSize is how many swatch colours profiles can choose from.
func PaletteSize() int { return len(swatchPalette) }

// gap is the empty space in pixels between adjacent swatches.
const gap = 4

// MultiProfileIcon renders n compact square swatches side by side, one per
// profile, for the "Show Multiple Profiles" tray display. activeIndex (or any
// out-of-range value to mean "none") gets a bright ring so the active account
// stays identifiable at a glance. n < 1 falls back to a single swatch.
func MultiProfileIcon(n, activeIndex int) []byte {
	colors := make([]int, max(n, 1))
	for i := range colors {
		colors[i] = i
	}
	return MultiProfileIconColors(colors, activeIndex)
}

// MultiProfileIconColors is MultiProfileIcon with an explicit palette index
// per swatch, so a profile keeps the colour it was given.
func MultiProfileIconColors(colors []int, activeIndex int) []byte {
	n := len(colors)
	if n < 1 {
		n = 1
		colors = []int{0}
	}
	width := n*size + (n-1)*gap
	img := image.NewRGBA(image.Rect(0, 0, width, size))
	accent := color.RGBA{0xFF, 0xC8, 0x8A, 0xFF}

	for i := 0; i < n; i++ {
		ox := i * (size + gap)
		c := swatchPalette[colors[i]%len(swatchPalette)]
		const r = 6
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if !inRoundedRect(x, y, size, size, r) {
					continue
				}
				img.Set(ox+x, y, c)
			}
		}
		if i == activeIndex {
			drawRing(img, ox, 0, size, size, r, 3, accent)
		}
	}
	return encode(img)
}

// drawRing outlines a rounded rect's border thickness px wide, used to mark
// the active profile's swatch in MultiProfileIcon.
func drawRing(img *image.RGBA, ox, oy, w, h, r, thickness int, c color.RGBA) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !inRoundedRect(x, y, w, h, r) {
				continue
			}
			edge := x < thickness || y < thickness || x >= w-thickness || y >= h-thickness
			if edge {
				img.Set(ox+x, oy+y, c)
			}
		}
	}
}

// drawDot fills a small filled circle of radius rad at (px,py).
func drawDot(img *image.RGBA, px, py, rad int, c color.RGBA) {
	for y := py - rad; y <= py+rad; y++ {
		for x := px - rad; x <= px+rad; x++ {
			if x < 0 || y < 0 || x >= size || y >= size {
				continue
			}
			if sq(x-px)+sq(y-py) <= sq(rad) {
				img.Set(x, y, c)
			}
		}
	}
}

// inRoundedRect reports whether (x,y) is inside a w×h rectangle with corner
// radius r.
func inRoundedRect(x, y, w, h, r int) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	// Distance into the rect from each edge; only corners need rounding.
	cx, cy := x, y
	if x < r && y < r {
		return sq(r-1-cx)+sq(r-1-cy) <= sq(r-1)
	}
	if x >= w-r && y < r {
		return sq(cx-(w-r))+sq(r-1-cy) <= sq(r-1)
	}
	if x < r && y >= h-r {
		return sq(r-1-cx)+sq(cy-(h-r)) <= sq(r-1)
	}
	if x >= w-r && y >= h-r {
		return sq(cx-(w-r))+sq(cy-(h-r)) <= sq(r-1)
	}
	return true
}

func sq(v int) int { return v * v }

// Swatch renders profile i's palette colour as a rounded square, matching its
// swatch in MultiProfileIcon so menu rows and the tray strip line up.
func Swatch(i int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := swatchPalette[i%len(swatchPalette)]
	const inset, r = 6, 5
	w := size - 2*inset
	for y := inset; y < size-inset; y++ {
		for x := inset; x < size-inset; x++ {
			if inRoundedRect(x-inset, y-inset, w, w, r) {
				img.Set(x, y, c)
			}
		}
	}
	// Grey outline keeps the near-black swatch visible on a dark menu.
	drawRing(img, inset, inset, w, w, r, 2, color.RGBA{0x8E, 0x8E, 0x93, 0xFF})
	return encode(img)
}

// gaugeColor is green under 50, amber under 80, red from 80 up.
func gaugeColor(pct float64) color.RGBA {
	switch {
	case pct >= 80:
		return color.RGBA{0xFF, 0x45, 0x3A, 0xFF}
	case pct >= 50:
		return color.RGBA{0xFF, 0x9F, 0x0A, 0xFF}
	}
	return color.RGBA{0x34, 0xC7, 0x59, 0xFF}
}

// drawGauge paints a ring centred on (cx, cy), filled clockwise from 12
// o'clock to pct (0–100) in gaugeColor over a grey track.
func drawGauge(img *image.RGBA, cx, cy, outer, inner, pct float64) {
	fill := gaugeColor(pct)
	track := color.RGBA{0x8E, 0x8E, 0x93, 0x66}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Hypot(dx, dy)
			if d > outer || d < inner {
				continue
			}
			ang := math.Atan2(dx, -dy) // 0 at 12 o'clock, clockwise
			if ang < 0 {
				ang += 2 * math.Pi
			}
			if ang/(2*math.Pi)*100 < pct {
				img.Set(x, y, fill)
			} else {
				img.Set(x, y, track)
			}
		}
	}
}

// Gauge renders a full-size usage ring for a menu row.
func Gauge(pct float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawGauge(img, size/2, size/2, 14, 9.5, pct)
	return encode(img)
}

// TrayGauge is the tray icon with a small usage ring in its bottom-right
// corner, on a dark disc so it reads against any menu-bar background.
func TrayGauge(pct float64) []byte {
	img := baseImage()
	const cx, cy = 22.0, 22.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) <= 10 {
				img.Set(x, y, color.RGBA{0x1C, 0x1C, 0x1E, 0xFF})
			}
		}
	}
	drawGauge(img, cx, cy, 8.5, 5, pct)
	return encode(img)
}
