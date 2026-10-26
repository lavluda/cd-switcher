package icon

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestMultiProfileIcon(t *testing.T) {
	cases := []struct {
		n, active int
		wantWidth int
	}{
		{1, 0, size},
		{2, 0, size*2 + gap},
		{2, -1, size*2 + gap},
		{3, 5, size*3 + gap*2}, // out-of-range active index: no ring, still valid
		{0, 0, size},           // n<1 clamps to 1
	}
	for _, c := range cases {
		data := MultiProfileIcon(c.n, c.active)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("MultiProfileIcon(%d, %d): invalid PNG: %v", c.n, c.active, err)
		}
		if got := img.Bounds().Dx(); got != c.wantWidth {
			t.Errorf("MultiProfileIcon(%d, %d): width = %d, want %d", c.n, c.active, got, c.wantWidth)
		}
		if got := img.Bounds().Dy(); got != size {
			t.Errorf("MultiProfileIcon(%d, %d): height = %d, want %d", c.n, c.active, got, size)
		}
	}
}

func TestMultiProfileIconDistinctSwatches(t *testing.T) {
	data := MultiProfileIcon(2, -1)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("invalid PNG: %v", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		conv := image.NewRGBA(img.Bounds())
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				conv.Set(x, y, img.At(x, y))
			}
		}
		rgba = conv
	}
	left := rgba.RGBAAt(size/2, size/2)
	right := rgba.RGBAAt(size+gap+size/2, size/2)
	if left == right {
		t.Errorf("adjacent swatches should differ: both are %v", left)
	}
}

func TestGaugeColours(t *testing.T) {
	// Pixel just right of 12 o'clock on the ring is filled for any pct > ~1.
	at := func(pct float64) (r, g uint32) {
		img, err := png.Decode(bytes.NewReader(Gauge(pct)))
		if err != nil {
			t.Fatal(err)
		}
		r, g, _, _ = img.At(size/2, 4).RGBA()
		return r >> 8, g >> 8
	}
	if r, g := at(20); g != 0xC7 || r != 0x34 {
		t.Errorf("20%%: got r=%x g=%x, want green", r, g)
	}
	if r, g := at(60); r != 0xFF || g != 0x9F {
		t.Errorf("60%%: got r=%x g=%x, want amber", r, g)
	}
	if r, g := at(85); r != 0xFF || g != 0x45 {
		t.Errorf("85%%: got r=%x g=%x, want red", r, g)
	}
}
