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

func hudTextObj(s string, size float32, bold bool, c color.Color) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle.Bold = bold
	return t
}

// paySubtleBox wraps content in a soft gray nested surface.
func paySubtleBox(padV, padH float32, radius float32, fill color.Color, body fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(fill)
	bg.CornerRadius = radius
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(padV, padV, padH, padH), body))
}

// payBrandBadge draws the rounded royal-blue logo badge on the top-left.
func payBrandBadge() fyne.CanvasObject {
	bg := canvas.NewRectangle(homePalette().blue)
	bg.CornerRadius = 7
	bg.SetMinSize(fyne.NewSize(22, 22))
	lbl := hudTextObj("C", 12, true, color.White)
	lbl.Alignment = fyne.TextAlignCenter
	return container.NewStack(bg, container.NewCenter(lbl))
}

// payButton is a clickable pill button used across the top bar, filter strips, and cards.
type payButton struct {
	widget.BaseWidget
	label          string
	fg, bg, border color.Color
	hoverBg        color.Color
	textSize       float32
	bold           bool
	padV, padH     float32
	radius         float32
	tap            func()
	hover, pressed bool
}

func newPayButton(label string, fg, bg, border, hoverBg color.Color, textSize float32, bold bool, padV, padH, radius float32, tap func()) *payButton {
	b := &payButton{
		label: label, fg: fg, bg: bg, border: border, hoverBg: hoverBg,
		textSize: textSize, bold: bold, padV: padV, padH: padH, radius: radius, tap: tap,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *payButton) Tapped(*fyne.PointEvent) {
	if b.tap != nil {
		b.tap()
	}
}
func (b *payButton) MouseIn(*desktop.MouseEvent)    { b.hover = true; b.Refresh() }
func (b *payButton) MouseMoved(*desktop.MouseEvent) {}
func (b *payButton) MouseOut()                      { b.hover, b.pressed = false, false; b.Refresh() }
func (b *payButton) MouseDown(*desktop.MouseEvent) {
	if b.tap != nil {
		b.pressed = true
		b.Refresh()
	}
}
func (b *payButton) MouseUp(*desktop.MouseEvent) { b.pressed = false; b.Refresh() }
func (b *payButton) Cursor() desktop.Cursor {
	if b.tap != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (b *payButton) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(b.bg)
	rect.CornerRadius = b.radius
	if b.border != nil {
		rect.StrokeColor = b.border
		rect.StrokeWidth = 1
	}
	txt := hudTextObj(b.label, b.textSize, b.bold, b.fg)
	shade := canvas.NewRectangle(color.Transparent)
	shade.CornerRadius = b.radius
	r := &payBtnRenderer{b: b, rect: rect, shade: shade, txt: txt}
	r.Refresh()
	return r
}

type payBtnRenderer struct {
	b     *payButton
	rect  *canvas.Rectangle
	shade *canvas.Rectangle // tints the fill on hover and press
	txt   *canvas.Text
}

// payShade is the tint laid over a button: light in dark mode, dark in light mode.
func payShade(alpha uint8) color.Color {
	if uitheme.Dark() {
		return color.NRGBA{255, 255, 255, alpha}
	}
	return color.NRGBA{0, 0, 0, alpha}
}

func (r *payBtnRenderer) Layout(size fyne.Size) {
	r.rect.Resize(size)
	r.shade.Resize(size)
	ts := r.txt.MinSize()
	r.txt.Move(fyne.NewPos((size.Width-ts.Width)/2, (size.Height-ts.Height)/2))
}

func (r *payBtnRenderer) MinSize() fyne.Size {
	ts := r.txt.MinSize()
	return fyne.NewSize(ts.Width+r.b.padH*2, ts.Height+r.b.padV*2)
}

func (r *payBtnRenderer) Refresh() {
	r.txt.Text = r.b.label
	r.txt.Color = r.b.fg
	if r.b.hover && r.b.hoverBg != nil {
		r.rect.FillColor = r.b.hoverBg
	} else {
		r.rect.FillColor = r.b.bg
	}
	switch {
	case r.b.pressed:
		r.shade.FillColor = payShade(0x26)
	case r.b.hover && r.b.tap != nil && r.b.hoverBg == r.b.bg:
		// No hover colour of its own (the grey buttons): tint instead.
		r.shade.FillColor = payShade(0x12)
	default:
		r.shade.FillColor = color.Transparent
	}
	r.rect.Refresh()
	r.shade.Refresh()
	r.txt.Refresh()
}

func (r *payBtnRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.rect, r.shade, r.txt}
}
func (r *payBtnRenderer) Destroy() {}

// payInteractiveRow is a clickable bento list item with hover highlight.
type payInteractiveRow struct {
	widget.BaseWidget
	body      fyne.CanvasObject
	baseFill  color.Color
	hoverFill color.Color
	radius    float32
	padV      float32
	padH      float32
	tap       func()
	hover     bool
}

func newPayInteractiveRow(body fyne.CanvasObject, baseFill, hoverFill color.Color, radius, padV, padH float32, tap func()) *payInteractiveRow {
	r := &payInteractiveRow{
		body: body, baseFill: baseFill, hoverFill: hoverFill,
		radius: radius, padV: padV, padH: padH, tap: tap,
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *payInteractiveRow) Tapped(*fyne.PointEvent) {
	if r.tap != nil {
		r.tap()
	}
}
func (r *payInteractiveRow) MouseIn(*desktop.MouseEvent)    { r.hover = true; r.Refresh() }
func (r *payInteractiveRow) MouseMoved(*desktop.MouseEvent) {}
func (r *payInteractiveRow) MouseOut()                      { r.hover = false; r.Refresh() }
func (r *payInteractiveRow) Cursor() desktop.Cursor {
	if r.tap != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (r *payInteractiveRow) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(r.baseFill)
	bg.CornerRadius = r.radius
	padded := container.New(layout.NewCustomPaddedLayout(r.padV, r.padV, r.padH, r.padH), r.body)
	return &payInteractiveRowRenderer{r: r, bg: bg, padded: padded}
}

type payInteractiveRowRenderer struct {
	r      *payInteractiveRow
	bg     *canvas.Rectangle
	padded fyne.CanvasObject
}

func (rr *payInteractiveRowRenderer) Layout(size fyne.Size) {
	rr.bg.Resize(size)
	rr.padded.Resize(size)
	rr.padded.Move(fyne.NewPos(0, 0))
}

func (rr *payInteractiveRowRenderer) MinSize() fyne.Size {
	return rr.padded.MinSize()
}

func (rr *payInteractiveRowRenderer) Refresh() {
	if rr.r.hover && rr.r.hoverFill != nil {
		rr.bg.FillColor = rr.r.hoverFill
	} else {
		rr.bg.FillColor = rr.r.baseFill
	}
	rr.bg.Refresh()
	rr.padded.Refresh()
}

func (rr *payInteractiveRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.bg, rr.padded}
}
func (rr *payInteractiveRowRenderer) Destroy() {}

// avatarCircle renders a circular badge with 2-letter initials, used in the
// top profile pill and the "Recent sessions & activity" table.
func avatarCircle(initials string, fg, bg color.Color, diam float32) fyne.CanvasObject {
	circle := canvas.NewCircle(bg)
	lbl := hudTextObj(initials, max(8.5, diam*0.38), true, fg)
	lbl.Alignment = fyne.TextAlignCenter
	return container.NewCenter(container.NewGridWrap(fyne.NewSize(diam, diam), container.NewStack(circle, container.NewCenter(lbl))))
}

// bentoRow3 lays out 3 columns in a single horizontal row where the left and
// right columns have fixed widths and the center column takes the remaining space
// (or vice-versa when flexIdx is 0 or 2).
type bentoCols struct {
	widths []float32 // 0 means flexible
	gap    float32
	minH   float32
}

func (b bentoCols) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	n := len(objs)
	if n == 0 {
		return
	}
	totalGap := b.gap * float32(max(n-1, 0))
	fixed := float32(0)
	flexCount := 0
	for i := 0; i < n; i++ {
		if i < len(b.widths) && b.widths[i] > 0 {
			fixed += b.widths[i]
		} else {
			flexCount++
		}
	}
	flexW := float32(0)
	if flexCount > 0 {
		flexW = max(80, (size.Width-fixed-totalGap)/float32(flexCount))
	}
	x := float32(0)
	h := max(size.Height, b.minH)
	for i, o := range objs {
		w := flexW
		if i < len(b.widths) && b.widths[i] > 0 {
			w = b.widths[i]
		}
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos(x, 0))
		x += w + b.gap
	}
}

func (b bentoCols) MinSize(objs []fyne.CanvasObject) fyne.Size {
	n := len(objs)
	if n == 0 {
		return fyne.NewSize(0, b.minH)
	}
	w := b.gap * float32(max(n-1, 0))
	h := b.minH
	for i, o := range objs {
		ms := o.MinSize()
		if i < len(b.widths) && b.widths[i] > 0 {
			w += b.widths[i]
		} else {
			w += ms.Width
		}
		if ms.Height > h {
			h = ms.Height
		}
	}
	return fyne.NewSize(w, h)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
