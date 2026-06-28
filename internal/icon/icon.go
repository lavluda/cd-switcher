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
		cx := float64(size)/2 + math.Cos(ang)*float64(size)*0.28
		cy := float64(size)/2 + math.Sin(ang)*float64(size)*0.28
		drawDot(img, int(cx), int(cy), 4, accent)
		frames[f] = encode(img)
	}
	return frames
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
