package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/uitheme"
)

// toggleSwitch is a pill-shaped on/off control in the macOS style, used in
// place of Fyne's stock square checkbox wherever Settings exposes a plain
// on/off preference (launch at login, show multiple profiles, auto-switch).
type toggleSwitch struct {
	widget.BaseWidget
	on       bool
	disabled bool
	onChange func(bool)
}

func newToggleSwitch(on bool, onChange func(bool)) *toggleSwitch {
	t := &toggleSwitch{on: on, onChange: onChange}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggleSwitch) SetDisabled(d bool) {
	t.disabled = d
	t.Refresh()
}

func (t *toggleSwitch) MinSize() fyne.Size { return fyne.NewSize(42, 24) }

func (t *toggleSwitch) Tapped(_ *fyne.PointEvent) {
	if t.disabled {
		return
	}
	t.on = !t.on
	t.Refresh()
	if t.onChange != nil {
		t.onChange(t.on)
	}
}

// Cursor marks the switch as clickable when hovered.
func (t *toggleSwitch) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (t *toggleSwitch) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(color.Transparent)
	knob := canvas.NewCircle(color.White)
	knob.StrokeWidth = 1
	r := &toggleRenderer{t: t, track: track, knob: knob}
	r.Refresh()
	return r
}

type toggleRenderer struct {
	t     *toggleSwitch
	track *canvas.Rectangle
	knob  *canvas.Circle
}

func (r *toggleRenderer) Layout(size fyne.Size) {
	r.track.Resize(size)
	r.track.CornerRadius = size.Height / 2

	const pad = 2
	d := size.Height - pad*2
	x := float32(pad)
	if r.t.on {
		x = size.Width - pad - d
	}
	r.knob.Resize(fyne.NewSize(d, d))
	r.knob.Move(fyne.NewPos(x, pad))
}

func (r *toggleRenderer) MinSize() fyne.Size { return r.t.MinSize() }

func (r *toggleRenderer) Refresh() {
	switch {
	case r.t.disabled:
		c := uitheme.CurrentTrackOff()
		r.track.FillColor = fadeColor(c, 0.6)
	case r.t.on:
		r.track.FillColor = uitheme.Accent
	default:
		r.track.FillColor = uitheme.CurrentTrackOff()
	}
	r.track.StrokeWidth = 0
	// A hairline ring around the knob keeps it legible even when its fill
	// (always white) sits close in value to the track behind it.
	r.knob.StrokeColor = uitheme.CurrentGridLine()
	r.track.Refresh()
	r.knob.Refresh()
	r.Layout(r.t.Size())
}

func (r *toggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.knob}
}

func (r *toggleRenderer) Destroy() {}

// fadeColor scales a color's opacity, used to visually dim a disabled toggle
// track without needing a second color pair per variant.
func fadeColor(c color.Color, alpha float32) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(255 * alpha)}
}

// segmentedControl is a single pill divided into equal-width options, one
// highlighted with the accent fill — the native macOS segmented-control look,
// in place of two separate full-width buttons side by side.
type segmentedControl struct {
	widget.BaseWidget
	options  []string
	selected int
	onChange func(int)
}

func newSegmentedControl(options []string, selected int, onChange func(int)) *segmentedControl {
	s := &segmentedControl{options: options, selected: selected, onChange: onChange}
	s.ExtendBaseWidget(s)
	return s
}

func (s *segmentedControl) SetSelected(i int) {
	if i == s.selected {
		return
	}
	s.selected = i
	s.Refresh()
}

func (s *segmentedControl) MinSize() fyne.Size { return fyne.NewSize(240, 36) }

// Cursor marks the control as clickable when hovered.
func (s *segmentedControl) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (s *segmentedControl) Tapped(e *fyne.PointEvent) {
	n := len(s.options)
	if n == 0 {
		return
	}
	segW := s.Size().Width / float32(n)
	idx := int(e.Position.X / segW)
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	if idx == s.selected {
		return
	}
	s.selected = idx
	s.Refresh()
	if s.onChange != nil {
		s.onChange(idx)
	}
}

func (s *segmentedControl) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(color.Transparent)
	pill := canvas.NewRectangle(uitheme.Accent)
	labels := make([]*canvas.Text, len(s.options))
	for i, opt := range s.options {
		labels[i] = canvas.NewText(opt, color.Black)
		labels[i].Alignment = fyne.TextAlignCenter
		labels[i].TextStyle = fyne.TextStyle{Bold: true}
	}
	r := &segmentedRenderer{s: s, track: track, pill: pill, labels: labels}
	r.Refresh()
	return r
}

type segmentedRenderer struct {
	s      *segmentedControl
	track  *canvas.Rectangle
	pill   *canvas.Rectangle
	labels []*canvas.Text
}

func (r *segmentedRenderer) Layout(size fyne.Size) {
	r.track.Resize(size)
	r.track.CornerRadius = size.Height / 2

	n := len(r.s.options)
	if n == 0 {
		return
	}
	segW := size.Width / float32(n)

	const pad = 3
	pillX := float32(r.s.selected)*segW + pad
	r.pill.Move(fyne.NewPos(pillX, pad))
	r.pill.Resize(fyne.NewSize(segW-pad*2, size.Height-pad*2))
	r.pill.CornerRadius = (size.Height - pad*2) / 2

	for i, l := range r.labels {
		l.TextSize = 13
		sz := l.MinSize()
		x := float32(i)*segW + (segW-sz.Width)/2
		y := (size.Height - sz.Height) / 2
		l.Move(fyne.NewPos(x, y))
		l.Resize(sz)
	}
}

func (r *segmentedRenderer) MinSize() fyne.Size { return r.s.MinSize() }

func (r *segmentedRenderer) Refresh() {
	r.track.FillColor = uitheme.CurrentTrackOff()
	r.track.Refresh()
	r.pill.FillColor = uitheme.Accent
	r.pill.Refresh()
	for i, l := range r.labels {
		if i == r.s.selected {
			l.Color = color.White
		} else {
			l.Color = uitheme.CurrentForeground()
		}
		l.Refresh()
	}
	r.Layout(r.s.Size())
}

func (r *segmentedRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, len(r.labels)+2)
	objs = append(objs, r.track, r.pill)
	for _, l := range r.labels {
		objs = append(objs, l)
	}
	return objs
}

func (r *segmentedRenderer) Destroy() {}

// vbox stacks items vertically with an explicit gap between them, instead of
// container.NewVBox's fixed theme.Padding() spacing. Plain NewVBox was
// quietly doubling up with hand-placed vGap() spacers (each vGap added its
// own height *on top of* NewVBox's automatic inter-child padding), which is
// what made the settings window feel too loose — this makes the gap the only
// source of space, so callers can size it deliberately.
func vbox(gap float32, items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedVBoxLayout(gap), items...)
}

// badge is a small tinted capsule label — an "Active" or "Most room" marker —
// in place of plain bold accent-colored text, matching Claude's soft-wash
// status chips instead of a solid fill.
func badge(text string) fyne.CanvasObject {
	lbl := canvas.NewText(text, uitheme.Accent)
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	lbl.TextSize = 11

	bg := canvas.NewRectangle(uitheme.AccentSoft)
	bg.CornerRadius = 8
	padded := container.New(layout.NewCustomPaddedLayout(3, 3, 10, 10), lbl)
	return container.NewStack(bg, padded)
}

// headingText renders a canvas.Text sized and colored for a page title —
// bigger and bolder than any Fyne stock label, for the "Settings" headline.
func headingText(s string) *canvas.Text {
	t := canvas.NewText(s, uitheme.CurrentForeground())
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = 24
	return t
}
