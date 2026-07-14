package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// sparkline is a minimal custom Fyne widget drawing a line chart of 5-hour
// utilization % over time. Fyne has no chart widget, and a plain
// container.NewWithoutLayout doesn't report a real MinSize or reflow when
// its container is resized (e.g. the window is maximized) — a proper
// widget.BaseWidget + WidgetRenderer does both correctly.
type sparkline struct {
	widget.BaseWidget
	samples []usage.Sample
}

func newSparkline(samples []usage.Sample) *sparkline {
	s := &sparkline{samples: samples}
	s.ExtendBaseWidget(s)
	return s
}

func (s *sparkline) MinSize() fyne.Size { return fyne.NewSize(1, 70) }

func (s *sparkline) CreateRenderer() fyne.WidgetRenderer {
	r := &sparklineRenderer{spark: s}
	r.build()
	return r
}

type sparklineRenderer struct {
	spark    *sparkline
	grid     *canvas.Line
	segments []*canvas.Line
	dot      *canvas.Circle
	empty    *widget.Label
}

func (r *sparklineRenderer) build() {
	if len(r.spark.samples) < 2 {
		r.empty = widget.NewLabel("Collecting usage history…")
		r.empty.Importance = widget.LowImportance
		return
	}
	r.grid = canvas.NewLine(uitheme.CurrentGridLine())
	r.dot = canvas.NewCircle(uitheme.Accent)
	r.segments = make([]*canvas.Line, len(r.spark.samples)-1)
	for i := range r.segments {
		line := canvas.NewLine(uitheme.Accent)
		line.StrokeWidth = 2
		r.segments[i] = line
	}
}

func (r *sparklineRenderer) Layout(size fyne.Size) {
	if r.empty != nil {
		r.empty.Resize(size)
		return
	}
	samples := r.spark.samples

	const pad float32 = 4
	plotW, plotH := size.Width-2*pad, size.Height-2*pad
	if plotW <= 0 || plotH <= 0 {
		return
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

	point := func(s usage.Sample) fyne.Position {
		x := pad + plotW*float32(s.Time.Sub(minT).Seconds()/span)
		y := pad + plotH*(1-float32(s.FiveHour)/100)
		return fyne.NewPos(x, y)
	}

	gy := pad + plotH*0.5
	r.grid.Position1 = fyne.NewPos(pad, gy)
	r.grid.Position2 = fyne.NewPos(pad+plotW, gy)
	r.grid.Refresh()

	prev := point(samples[0])
	for i, s := range samples[1:] {
		p := point(s)
		line := r.segments[i]
		line.Position1 = prev
		line.Position2 = p
		line.Refresh()
		prev = p
	}

	const dr float32 = 3
	r.dot.Move(fyne.NewPos(prev.X-dr, prev.Y-dr))
	r.dot.Resize(fyne.NewSize(2*dr, 2*dr))
}

func (r *sparklineRenderer) MinSize() fyne.Size { return r.spark.MinSize() }

func (r *sparklineRenderer) Refresh() {
	r.Layout(r.spark.Size())
	canvas.Refresh(r.spark)
}

func (r *sparklineRenderer) Objects() []fyne.CanvasObject {
	if r.empty != nil {
		return []fyne.CanvasObject{r.empty}
	}
	objs := make([]fyne.CanvasObject, 0, len(r.segments)+2)
	objs = append(objs, r.grid)
	objs = append(objs, r.dot)
	for _, l := range r.segments {
		objs = append(objs, l)
	}
	return objs
}

func (r *sparklineRenderer) Destroy() {}
