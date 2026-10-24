package main

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// sparkline is a minimal custom Fyne widget drawing a filled area chart of
// 5-hour utilization % over time. Fyne has no chart widget, and a plain
// container.NewWithoutLayout doesn't report a real MinSize or reflow when its
// container is resized (e.g. the window is maximized) — a proper
// widget.BaseWidget + WidgetRenderer does both correctly.
//
// The chart draws itself as a framed plot rather than a bare trace: a bordered
// box, a fixed 0–100% y-axis with labelled gridlines, and the time range
// marked along the bottom. Without those markings the line has no scale, so a
// flat 5% stretch and a flat 80% one look identical.
type sparkline struct {
	widget.BaseWidget
	samples []usage.Sample
}

func newSparkline(samples []usage.Sample) *sparkline {
	s := &sparkline{samples: samples}
	s.ExtendBaseWidget(s)
	return s
}

// Chart geometry, in logical (pre-scaling) pixels. The plot area is inset
// inside the frame by a gutter on the left for the % labels and a row at the
// bottom for the time labels.
const (
	chartGutter   = 34 // left column holding the 0/50/100% axis labels
	chartPadRight = 8
	chartPadTop   = 8
	chartAxisRow  = 15 // bottom row holding the start/end time labels
	chartPlotMin  = 56 // smallest the plot area itself may get
	chartLabelSz  = 10
)

func (s *sparkline) MinSize() fyne.Size {
	return fyne.NewSize(chartGutter+90+chartPadRight,
		chartPadTop+chartPlotMin+2+chartAxisRow)
}

func (s *sparkline) CreateRenderer() fyne.WidgetRenderer {
	r := &sparklineRenderer{spark: s}
	if len(s.samples) < 2 {
		r.empty = widget.NewLabel("Collecting usage history…")
		r.empty.Importance = widget.LowImportance
		return r
	}

	r.frame = canvas.NewRectangle(color.Transparent)
	r.frame.StrokeColor = uitheme.CurrentGridLine()
	r.frame.StrokeWidth = 1
	r.frame.CornerRadius = 6

	r.raster = canvas.NewRaster(r.generate)
	r.dot = canvas.NewCircle(uitheme.Accent)

	for i, text := range [3]string{"100%", "50%", "0%"} {
		r.yLabels[i] = newChartText(text, fyne.TextAlignTrailing)
	}
	r.tStart = newChartText(s.samples[0].Time.Local().Format("3:04pm"), fyne.TextAlignLeading)
	r.tEnd = newChartText("now", fyne.TextAlignTrailing)
	return r
}

func newChartText(text string, align fyne.TextAlign) *canvas.Text {
	t := canvas.NewText(text, uitheme.CurrentMuted())
	t.TextSize = chartLabelSz
	t.Alignment = align
	return t
}

type sparklineRenderer struct {
	spark   *sparkline
	frame   *canvas.Rectangle
	raster  *canvas.Raster
	dot     *canvas.Circle
	yLabels [3]*canvas.Text // 100% / 50% / 0%, top to bottom
	tStart  *canvas.Text
	tEnd    *canvas.Text
	empty   *widget.Label

	// plotW is the plot area's logical width, recorded at layout time so
	// generate can work out the display's pixel scale (the raster is handed
	// device pixels, which on a Retina screen is 2x the logical size).
	plotW float32
}

// plotRect returns the position and size of the plot area inside the frame.
func plotRect(size fyne.Size) (fyne.Position, fyne.Size) {
	w := size.Width - chartGutter - chartPadRight
	h := size.Height - chartPadTop - chartAxisRow - 2
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return fyne.NewPos(chartGutter, chartPadTop), fyne.NewSize(w, h)
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// generate rasterizes the plot interior in one pass: gridlines at every 25%
// and every quarter of the time span, then a gradient fill under the line
// fading out toward the baseline, with the line itself drawn as a narrow
// anti-aliased band — the filled-area look of an analytics dashboard chart,
// instead of a bare zigzag of line segments.
func (r *sparklineRenderer) generate(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	samples := r.spark.samples
	if w < 2 || h < 2 || len(samples) < 2 {
		return img
	}

	scale := 1.0
	if r.plotW > 0 {
		scale = float64(w) / float64(r.plotW)
		if scale < 1 {
			scale = 1
		}
	}
	thick := int(math.Round(scale))
	if thick < 1 {
		thick = 1
	}

	grid := toRGBA(uitheme.CurrentGridLine())
	dash := int(math.Round(4 * scale))
	if dash < 2 {
		dash = 2
	}
	on := func(i int) bool { return (i/dash)%2 == 0 }

	// Horizontal gridlines every 25%; the 0% baseline and the left axis are
	// solid so the plot reads as a boxed chart, the rest dashed so they stay
	// behind the data.
	for _, pct := range []float64{25, 50, 75, 100} {
		y := int(math.Round((1 - pct/100) * float64(h-1)))
		for t := 0; t < thick; t++ {
			yy := y + t
			if yy < 0 || yy >= h {
				continue
			}
			for x := 0; x < w; x++ {
				if on(x) {
					over(img, x, yy, blend(grid, 0.85))
				}
			}
		}
	}
	// Vertical gridlines at each quarter of the time span.
	for _, frac := range []float64{0.25, 0.5, 0.75} {
		x := int(math.Round(frac * float64(w-1)))
		for t := 0; t < thick; t++ {
			xx := x + t
			if xx < 0 || xx >= w {
				continue
			}
			for y := 0; y < h; y++ {
				if on(y) {
					over(img, xx, y, blend(grid, 0.55))
				}
			}
		}
	}
	for t := 0; t < thick; t++ {
		for x := 0; x < w; x++ { // 0% baseline
			over(img, x, h-1-t, blend(grid, 1))
		}
		for y := 0; y < h; y++ { // left axis
			over(img, t, y, blend(grid, 1))
		}
	}

	minT, maxT := samples[0].Time, samples[0].Time
	for _, s := range samples {
		if s.Time.Before(minT) {
			minT = s.Time
		}
		if s.Time.After(maxT) {
			maxT = s.Time
		}
	}
	span := maxT.Sub(minT).Seconds()
	if span <= 0 {
		span = 1
	}

	// One interpolated line-height per pixel column, linearly between the two
	// samples straddling that column's timestamp.
	lineY := make([]float64, w)
	si := 0
	for x := 0; x < w; x++ {
		target := float64(x) / float64(w-1) * span
		for si < len(samples)-2 && samples[si+1].Time.Sub(minT).Seconds() < target {
			si++
		}
		a, b := samples[si], samples[si+1]
		at, bt := a.Time.Sub(minT).Seconds(), b.Time.Sub(minT).Seconds()
		v := a.FiveHour
		if bt > at {
			f := (target - at) / (bt - at)
			if f < 0 {
				f = 0
			} else if f > 1 {
				f = 1
			}
			v = a.FiveHour + f*(b.FiveHour-a.FiveHour)
		}
		lineY[x] = (1 - clampPct(v)/100) * float64(h-1)
	}

	accent := toRGBA(uitheme.Accent)
	lineHalfWidth := 1.1 * scale // px either side of the interpolated line
	const fillMaxAlpha = 0.20    // opacity of the area fill right under the line

	for x := 0; x < w; x++ {
		ly := lineY[x]
		for y := 0; y < h; y++ {
			d := float64(y) - ly
			switch {
			case math.Abs(d) <= lineHalfWidth:
				over(img, x, y, blend(accent, 1-math.Abs(d)/lineHalfWidth*0.4))
			case d > lineHalfWidth:
				below := float64(h-1) - ly
				if below <= 0 {
					continue
				}
				fade := 1 - (d-lineHalfWidth)/below
				if fade < 0 {
					fade = 0
				}
				over(img, x, y, blend(accent, fade*fillMaxAlpha))
			}
		}
	}

	return img
}

func toRGBA(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

// blend returns c at the given opacity over a transparent background,
// pre-multiplying so image.RGBA composites it correctly.
func blend(c color.RGBA, alpha float64) color.RGBA {
	if alpha < 0 {
		alpha = 0
	} else if alpha > 1 {
		alpha = 1
	}
	a := uint8(alpha * 255)
	return color.RGBA{
		R: uint8(float64(c.R) * alpha),
		G: uint8(float64(c.G) * alpha),
		B: uint8(float64(c.B) * alpha),
		A: a,
	}
}

// over composites a pre-multiplied src pixel onto img, so the data drawn last
// lands on top of the gridlines instead of erasing them.
func over(img *image.RGBA, x, y int, src color.RGBA) {
	if src.A == 0 {
		return
	}
	if src.A == 0xff {
		img.SetRGBA(x, y, src)
		return
	}
	d := img.RGBAAt(x, y)
	ia := float64(255-src.A) / 255
	img.SetRGBA(x, y, color.RGBA{
		R: src.R + uint8(float64(d.R)*ia),
		G: src.G + uint8(float64(d.G)*ia),
		B: src.B + uint8(float64(d.B)*ia),
		A: src.A + uint8(float64(d.A)*ia),
	})
}

func (r *sparklineRenderer) Layout(size fyne.Size) {
	if r.empty != nil {
		r.empty.Resize(size)
		return
	}
	r.frame.Move(fyne.NewPos(0, 0))
	r.frame.Resize(size)

	pos, sz := plotRect(size)
	r.plotW = sz.Width
	r.raster.Move(pos)
	r.raster.Resize(sz)

	// % labels, each centred on the gridline it names.
	const labelH = chartLabelSz + 5
	for i, lbl := range r.yLabels {
		y := pos.Y + sz.Height*float32(i)/2
		lbl.Resize(fyne.NewSize(chartGutter-6, labelH))
		lbl.Move(fyne.NewPos(2, y-labelH/2))
	}

	// Time range, under the two ends of the plot.
	rowY := pos.Y + sz.Height + 2
	half := sz.Width / 2
	r.tStart.Resize(fyne.NewSize(half, chartAxisRow))
	r.tStart.Move(fyne.NewPos(pos.X, rowY))
	r.tEnd.Resize(fyne.NewSize(half, chartAxisRow))
	r.tEnd.Move(fyne.NewPos(pos.X+half, rowY))

	samples := r.spark.samples
	v := clampPct(samples[len(samples)-1].FiveHour)
	y := pos.Y + sz.Height*float32(1-v/100)

	const dr float32 = 3
	r.dot.Move(fyne.NewPos(pos.X+sz.Width-dr*2, y-dr))
	r.dot.Resize(fyne.NewSize(2*dr, 2*dr))
}

func (r *sparklineRenderer) MinSize() fyne.Size { return r.spark.MinSize() }

func (r *sparklineRenderer) Refresh() {
	if r.empty == nil {
		// Colors are hand-picked per theme variant, so they have to be
		// re-read when the user flips light/dark.
		r.frame.StrokeColor = uitheme.CurrentGridLine()
		r.dot.FillColor = uitheme.Accent
		muted := uitheme.CurrentMuted()
		for _, lbl := range r.yLabels {
			lbl.Color = muted
		}
		r.tStart.Color = muted
		r.tEnd.Color = muted
	}
	r.Layout(r.spark.Size())
	canvas.Refresh(r.spark)
}

func (r *sparklineRenderer) Objects() []fyne.CanvasObject {
	if r.empty != nil {
		return []fyne.CanvasObject{r.empty}
	}
	objs := []fyne.CanvasObject{r.frame, r.raster, r.dot, r.tStart, r.tEnd}
	for _, lbl := range r.yLabels {
		objs = append(objs, lbl)
	}
	return objs
}

func (r *sparklineRenderer) Destroy() {}
