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
	tint     color.Color // on-state colour; the theme accent when nil
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
		if r.t.tint != nil {
			r.track.FillColor = r.t.tint
		}
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

// vbox stacks items vertically with an explicit gap between them, instead of
// container.NewVBox's fixed theme.Padding() spacing. Plain NewVBox was
// quietly doubling up with hand-placed vGap() spacers (each vGap added its
// own height *on top of* NewVBox's automatic inter-child padding), which is
// what made the settings window feel too loose — this makes the gap the only
// source of space, so callers can size it deliberately.
func vbox(gap float32, items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedVBoxLayout(gap), items...)
}
