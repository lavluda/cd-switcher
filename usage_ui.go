package main

// The Usage page: tokens and estimated cost from the Claude Code and Codex
// session logs on this machine, by hour or day, model and project.

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/sessionlog"
	"github.com/lavluda/cd-switcher/internal/uitheme"
)

// usageState is the Usage page's data and choices. UI-thread only, except
// scanning, which guards the background scan.
type usageState struct {
	scanner   *sessionlog.Scanner
	scannedAt time.Time
	scanning  atomic.Bool
	rangeIdx  int // 0 today, 1 last 7 days, 2 last 30 days
	toolIdx   int // 0 all, 1 Claude Code, 2 Codex
	metric    int // what the chart shows: 0 cost, 1 requests, 2 tokens
	table     int // 0 by model, 1 by project
}

// codexColor marks Codex in the chart and tables; Claude Code uses the
// app's accent, so each tool keeps one color everywhere on the page.
var codexColor = color.NRGBA{R: 0x2B, G: 0x7A, B: 0x8C, A: 0xFF}

func toolColor(tool string) color.Color {
	switch tool {
	case sessionlog.Claude:
		return uitheme.Accent
	case sessionlog.Codex:
		return codexColor
	}
	return uitheme.CurrentMuted()
}

var usageTools = []string{"", sessionlog.Claude, sessionlog.Codex}

// chartIcon is the Usage page's sidebar icon, themed like the stock icons.
var chartIcon = theme.NewThemedResource(fyne.NewStaticResource("chart.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M4 20h16v-2H4zM5 16h3V9H5zm5.5 0h3V4h-3zm5.5 0h3v-5h-3z"/></svg>`)))

// usageNavItem is the sidebar's Usage entry with today's estimated cost at
// its right edge. Opening Settings starts a log read when the last one is
// more than five minutes old, so the figure is there before the page is.
func (a *app) usageNavItem(b *widget.Button) fyne.CanvasObject {
	u := &a.usage
	if u.scanner == nil {
		u.scanner = sessionlog.NewScanner()
	}
	if time.Since(u.scannedAt) > 5*time.Minute {
		a.scanUsage()
	}
	if u.scannedAt.IsZero() {
		return b
	}
	now := time.Now()
	today := u.scanner.Report(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), "", true).Total
	if today.Cost == 0 {
		return b
	}
	cost := canvas.NewText(money(today.Cost), uitheme.CurrentMuted())
	cost.TextSize = 12
	// The figure sits over the button; it takes no taps, so they reach it.
	// The spacer keeps room for it beside the label in a narrow sidebar.
	room := canvas.NewRectangle(color.Transparent)
	room.SetMinSize(fyne.NewSize(b.MinSize().Width+cost.MinSize().Width+16, 0))
	return container.NewStack(room, b, container.NewBorder(nil, nil, nil,
		container.New(layout.NewCustomPaddedLayout(0, 0, 0, 10), container.NewCenter(cost))))
}

// redrawSettings rebuilds the open Settings window after a choice on a page.
func (a *app) redrawSettings() {
	if a.settingsWin != nil {
		a.settingsWin.SetContent(a.settingsContent())
	}
}

// scanUsage reads new log lines in the background, then redraws.
func (a *app) scanUsage() {
	u := &a.usage
	if !u.scanning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer u.scanning.Store(false)
		if err := u.scanner.Scan(); err != nil {
			logf("usage scan: %v", err)
		}
		fyne.Do(func() {
			u.scannedAt = time.Now()
			a.redrawSettings()
		})
	}()
}

func (a *app) usageTabContent() fyne.CanvasObject {
	u := &a.usage
	if u.scanner == nil {
		u.scanner = sessionlog.NewScanner()
	}
	if time.Since(u.scannedAt) > time.Minute {
		a.scanUsage()
	}

	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		u.scannedAt = time.Time{}
		a.scanUsage()
	})
	refresh.Importance = widget.LowImportance
	status := "Reading logs…"
	if !u.scannedAt.IsZero() {
		status = "Updated " + u.scannedAt.Format("3:04pm")
	}
	header := tabHeader("Usage", "Estimated from the session logs Claude Code and Codex keep on this Mac, priced at API list rates.", mutedText(status), refresh)

	ranges := newQuietSegmented([]string{"Today", "7 days", "30 days"}, u.rangeIdx, func(i int) { u.rangeIdx = i; a.redrawSettings() })
	tools := newQuietSegmented([]string{"All tools", "Claude Code", "Codex"}, u.toolIdx, func(i int) { u.toolIdx = i; a.redrawSettings() })
	filters := container.NewHBox(ranges, layout.NewSpacer(), tools)

	if u.scannedAt.IsZero() {
		return scrollTab(header, filters, newPanel(lowLabel("Reading session logs. The first read takes a few seconds.")))
	}

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from, hourly := midnight, true
	switch u.rangeIdx {
	case 1:
		from, hourly = midnight.AddDate(0, 0, -6), false
	case 2:
		from, hourly = midnight.AddDate(0, 0, -29), false
	}
	rep := u.scanner.Report(from, usageTools[u.toolIdx], hourly)
	if rep.Total.Requests == 0 {
		return scrollTab(header, filters, newPanel(lowLabel("No Claude Code or Codex sessions in this range.")))
	}

	tot := rep.Total
	tiles := container.NewGridWithColumns(4,
		a.statTile("Estimated cost", money(tot.Cost), 0),
		a.statTile("Requests", count(int64(tot.Requests)), 1),
		a.statTile("Tokens", tokens(tot.Tokens()), 2),
		newStatTile("Cache hits", fmt.Sprintf("%.1f%%", tot.CacheHitRate()*100), false, nil),
	)
	chart := newBarChart(rep, u.metric)
	unit := "hour"
	if !hourly {
		unit = "day"
	}
	chartTitle := []string{"Cost", "Requests", "Tokens"}[u.metric] + " per " + unit
	legend := container.NewHBox()
	for i, name := range []string{"Claude Code", "Codex"} {
		if u.toolIdx == 0 || u.toolIdx == i+1 {
			legend.Add(legendItem(name, usageTools[i+1]))
		}
	}
	chartHead := container.NewBorder(nil, nil, widget.NewLabelWithStyle(chartTitle, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), legend)
	overview := newPanel(vbox(4, tiles, divider(), chartHead, chart))

	tabs := newQuietSegmented([]string{"By model", "By project"}, u.table, func(i int) { u.table = i; a.redrawSettings() })
	rows := rep.ByModel
	first := "Model"
	if u.table == 1 {
		rows, first = rep.ByProject, "Project"
	}
	table := usageTable(first, rows, tot)

	items := []fyne.CanvasObject{header, filters, overview, container.NewHBox(tabs), table}
	if len(rep.Unpriced) > 0 {
		names := make([]string, len(rep.Unpriced))
		for i, m := range rep.Unpriced {
			names[i] = modelName(m)
		}
		items = append(items, lowLabel("No list price is known for "+strings.Join(names, ", ")+", so their tokens are counted but not priced."))
	}
	return scrollTab(items...)
}

// statTile is one figure in the strip above the chart; tapping it charts it.
func (a *app) statTile(label, value string, metric int) fyne.CanvasObject {
	return newStatTile(label, value, a.usage.metric == metric, func() {
		a.usage.metric = metric
		a.redrawSettings()
	})
}

func legendItem(name, tool string) fyne.CanvasObject {
	dot := canvas.NewCircle(toolColor(tool))
	return container.NewHBox(container.NewCenter(container.NewGridWrap(fyne.NewSize(9, 9), dot)), mutedText(name))
}

// usageTable lists rows with their share of the total cost (or of tokens
// when nothing in range has a price).
func usageTable(first string, rows []sessionlog.Row, total sessionlog.Usage) fyne.CanvasObject {
	const limit = 12
	head := func(s string, align fyne.TextAlign) fyne.CanvasObject {
		t := canvas.NewText(s, uitheme.CurrentMuted())
		t.TextSize = 12
		t.Alignment = align
		return container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), t)
	}
	cols := tableCols{widths: []float32{90, 90, 96, 132}}
	objs := []fyne.CanvasObject{container.New(cols,
		head(first, fyne.TextAlignLeading), head("Requests", fyne.TextAlignTrailing), head("Tokens", fyne.TextAlignTrailing),
		head("Cost", fyne.TextAlignTrailing), head("Share", fyne.TextAlignLeading)), divider()}
	byCost := total.Cost > 0
	for i, r := range rows {
		if i == limit {
			objs = append(objs, lowLabel(fmt.Sprintf("and %d more", len(rows)-limit)))
			break
		}
		share := 0.0
		if byCost {
			share = r.Cost / total.Cost
		} else if total.Tokens() > 0 {
			share = float64(r.Tokens()) / float64(total.Tokens())
		}
		label := r.Name
		if first == "Model" {
			label = modelName(r.Name)
		}
		name := widget.NewLabel(label)
		name.Truncation = fyne.TextTruncateEllipsis
		dot := canvas.NewCircle(toolColor(r.Tool))
		nameCell := container.NewBorder(nil, nil, container.NewCenter(container.NewGridWrap(fyne.NewSize(8, 8), dot)), nil, name)
		cost := "—"
		if r.Cost > 0 {
			cost = money(r.Cost)
		}
		pct := mutedText(fmt.Sprintf("%.0f%%", share*100))
		bar := container.NewBorder(nil, nil, nil, pct, container.NewCenter(container.NewGridWrap(fyne.NewSize(72, 6), shareBar(share, r.Tool))))
		objs = append(objs, container.New(cols, nameCell, numLabel(count(int64(r.Requests))), numLabel(tokens(r.Tokens())), numLabel(cost), bar))
		if i < len(rows)-1 && i < limit-1 {
			objs = append(objs, divider())
		}
	}
	return newPanel(vbox(0, objs...))
}

// shareBar is a thin bar in the row's tool color, neutral for a row that
// mixes tools.
func shareBar(share float64, tool string) *thinMeter {
	m := newThinMeter(float32(share))
	m.fill = toolColor(tool)
	if tool == "" {
		m.fill = uitheme.CurrentForeground()
	}
	return m
}

// modelName turns a model ID into its product name: claude-opus-5-5 is
// "Opus 5.5", gpt-6.1-sol is "GPT-6.1 Sol". Unknown shapes pass through.
func modelName(id string) string {
	if rest, ok := strings.CutPrefix(id, "claude-"); ok {
		parts := strings.Split(rest, "-")
		var ver []string
		for _, p := range parts[1:] {
			if len(p) >= 8 { // a date snapshot suffix
				break
			}
			ver = append(ver, p)
		}
		return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + strings.Join(ver, ".")
	}
	if rest, ok := strings.CutPrefix(id, "gpt-"); ok {
		v, variant, _ := strings.Cut(rest, "-")
		name := "GPT-" + v
		if variant != "" {
			name += " " + strings.ToUpper(variant[:1]) + variant[1:]
		}
		return name
	}
	if id == "codex-auto-review" {
		return "Codex auto-review"
	}
	return id
}

func numLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Alignment = fyne.TextAlignTrailing
	return l
}

// tableCols lays a row out as one flexible first column and fixed-width
// columns after it, so every row of a table lines up.
type tableCols struct{ widths []float32 }

func (c tableCols) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	fixed := float32(0)
	for _, w := range c.widths {
		fixed += w
	}
	x := float32(0)
	for i, o := range objs {
		w := size.Width - fixed
		if i > 0 && i-1 < len(c.widths) {
			w = c.widths[i-1]
		}
		h := o.MinSize().Height
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos(x, (size.Height-h)/2))
		x += w
	}
}

func (c tableCols) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w, h := float32(120), float32(0)
	for _, cw := range c.widths {
		w += cw
	}
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(w, h)
}

// ---------- number formats ----------

func money(v float64) string {
	if v >= 1000 {
		return "$" + count(int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
}

func count(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return trimZero(float64(n)/1e9) + "B"
	case n >= 1e6:
		return trimZero(float64(n)/1e6) + "M"
	case n >= 1e3:
		return trimZero(float64(n)/1e3) + "K"
	}
	return fmt.Sprint(n)
}

// trimZero shows three significant figures: 448M, 2.46B, 12.3K.
func trimZero(v float64) string {
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
	}
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", v), "0"), ".0")
}

// ---------- stat tile ----------

type statTile struct {
	widget.BaseWidget
	label, value string
	selected     bool
	tapped       func()
}

func newStatTile(label, value string, selected bool, tapped func()) *statTile {
	t := &statTile{label: label, value: value, selected: selected, tapped: tapped}
	t.ExtendBaseWidget(t)
	return t
}

func (t *statTile) Tapped(*fyne.PointEvent) {
	if t.tapped != nil {
		t.tapped()
	}
}

func (t *statTile) Cursor() desktop.Cursor {
	if t.tapped == nil {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (t *statTile) CreateRenderer() fyne.WidgetRenderer {
	label := canvas.NewText(t.label, uitheme.CurrentMuted())
	label.TextSize = 12
	value := canvas.NewText(t.value, uitheme.CurrentForeground())
	value.TextSize = 26
	value.TextStyle = fyne.TextStyle{Bold: true}
	mark := canvas.NewRectangle(uitheme.CurrentForeground())
	mark.CornerRadius = 1
	if !t.selected {
		mark.Hide()
	}
	return &statTileRenderer{t: t, label: label, value: value, mark: mark}
}

type statTileRenderer struct {
	t            *statTile
	label, value *canvas.Text
	mark         *canvas.Rectangle
}

func (r *statTileRenderer) Layout(size fyne.Size) {
	r.label.Move(fyne.NewPos(8, 8))
	r.value.Move(fyne.NewPos(8, 8+r.label.MinSize().Height+2))
	r.mark.Resize(fyne.NewSize(size.Width-16, 2))
	r.mark.Move(fyne.NewPos(8, size.Height-3))
}

func (r *statTileRenderer) MinSize() fyne.Size {
	return fyne.NewSize(max(r.label.MinSize().Width, r.value.MinSize().Width)+16, r.label.MinSize().Height+r.value.MinSize().Height+22)
}

func (r *statTileRenderer) Refresh() {
	r.label.Color, r.value.Color, r.mark.FillColor = uitheme.CurrentMuted(), uitheme.CurrentForeground(), uitheme.CurrentForeground()
	r.label.Refresh()
	r.value.Refresh()
	r.mark.Refresh()
}

func (r *statTileRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.label, r.value, r.mark}
}

func (r *statTileRenderer) Destroy() {}

// ---------- bar chart ----------

// barChart draws one bar per hour or day, stacked by tool, with a light
// grid, and names the hovered bar's figures in a small card.
type barChart struct {
	widget.BaseWidget
	labels []string
	values [][2]float64 // per bucket: Claude Code, Codex
	tips   []string
	format func(float64) string
	hover  int
}

func newBarChart(rep sessionlog.Report, metric int) *barChart {
	pick := func(u sessionlog.Usage) float64 {
		switch metric {
		case 1:
			return float64(u.Requests)
		case 2:
			return float64(u.Tokens())
		}
		return u.Cost
	}
	format := []func(float64) string{
		func(v float64) string {
			if v >= 100 {
				return fmt.Sprintf("$%.0f", v)
			}
			return fmt.Sprintf("$%.2f", v)
		},
		func(v float64) string { return count(int64(math.Round(v))) },
		func(v float64) string { return tokens(int64(v)) },
	}[metric]
	c := &barChart{format: format, hover: -1}
	for _, b := range rep.Buckets {
		cl, cx := pick(b.Tools[sessionlog.Claude]), pick(b.Tools[sessionlog.Codex])
		label := b.Start.Format("15:04")
		when := b.Start.Format("15:04") + "–" + b.Start.Add(time.Hour).Format("15:04")
		if !rep.Hourly {
			label = b.Start.Format("Jan 2")
			when = b.Start.Format("Mon, Jan 2")
		}
		c.labels = append(c.labels, label)
		c.values = append(c.values, [2]float64{cl, cx})
		tip := when + "\nClaude Code  " + format(cl) + "\nCodex  " + format(cx)
		c.tips = append(c.tips, tip)
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *barChart) MinSize() fyne.Size { return fyne.NewSize(320, 210) }

func (c *barChart) MouseIn(e *desktop.MouseEvent)    { c.MouseMoved(e) }
func (c *barChart) MouseOut()                        { c.setHover(-1) }
func (c *barChart) MouseMoved(e *desktop.MouseEvent) { c.setHover(c.bucketAt(e.Position.X)) }

func (c *barChart) setHover(i int) {
	if i != c.hover {
		c.hover = i
		c.Refresh()
	}
}

// plot geometry: room on the left for the axis labels, below for dates.
const (
	chartLeft   = 44
	chartBottom = 22
	chartTop    = 8
)

func (c *barChart) bucketAt(x float32) int {
	n := len(c.values)
	w := c.Size().Width - chartLeft
	if n == 0 || w <= 0 || x < chartLeft {
		return -1
	}
	i := int((x - chartLeft) / (w / float32(n)))
	if i >= n {
		return -1
	}
	return i
}

// niceMax rounds up to 1, 2, 2.5 or 5 times a power of ten.
func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}

func (c *barChart) CreateRenderer() fyne.WidgetRenderer {
	r := &barChartRenderer{c: c}
	peak := 0.0
	for _, v := range c.values {
		peak = max(peak, v[0]+v[1])
	}
	r.top = niceMax(peak)
	for i := 0; i < 3; i++ {
		line := canvas.NewRectangle(uitheme.CurrentGridLine())
		r.grid = append(r.grid, line)
		r.axis = append(r.axis, newChartText(c.format(r.top*float64(i)/2), fyne.TextAlignTrailing))
	}
	r.hoverBg = canvas.NewRectangle(fadeColor(uitheme.CurrentForeground(), 0.05))
	r.hoverBg.CornerRadius = 4
	for i, v := range c.values {
		for s := 0; s < 2; s++ {
			bar := canvas.NewRectangle(toolColor(usageTools[s+1]))
			bar.CornerRadius = 2
			if v[s] == 0 {
				bar.Hide()
			}
			r.bars = append(r.bars, bar)
		}
		r.xlabels = append(r.xlabels, newChartText(c.labels[i], fyne.TextAlignCenter))
	}
	r.tipBg = canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	r.tipBg.CornerRadius = 6
	r.tipBg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	r.tipBg.StrokeWidth = 1
	for i := 0; i < 3; i++ {
		t := canvas.NewText("", uitheme.CurrentForeground())
		t.TextSize = 12
		r.tipText = append(r.tipText, t)
	}
	r.tipText[0].TextStyle = fyne.TextStyle{Bold: true}
	return r
}

type barChartRenderer struct {
	c       *barChart
	top     float64
	grid    []*canvas.Rectangle
	axis    []*canvas.Text
	bars    []*canvas.Rectangle
	xlabels []*canvas.Text
	hoverBg *canvas.Rectangle
	tipBg   *canvas.Rectangle
	tipText []*canvas.Text
}

func (r *barChartRenderer) Layout(size fyne.Size) {
	plotW := size.Width - chartLeft
	plotH := size.Height - chartBottom - chartTop
	n := len(r.c.values)
	for i, line := range r.grid {
		y := chartTop + plotH - plotH*float32(i)/2
		line.Resize(fyne.NewSize(plotW, 1))
		line.Move(fyne.NewPos(chartLeft, y))
		r.axis[i].Resize(fyne.NewSize(chartLeft-8, 14))
		r.axis[i].Move(fyne.NewPos(0, y-7))
	}
	if n == 0 {
		return
	}
	slot := plotW / float32(n)
	barW := max(min(slot*0.62, 28), 2)
	// Label every k-th bar so labels never overlap.
	every := int(math.Ceil(float64(52 / slot)))
	for i, v := range r.c.values {
		x := chartLeft + slot*float32(i) + (slot-barW)/2
		base := chartTop + plotH
		for s := 0; s < 2; s++ {
			h := float32(v[s]/r.top) * plotH
			if v[s] > 0 && h < 2 {
				h = 2
			}
			bar := r.bars[i*2+s]
			bar.Resize(fyne.NewSize(barW, h))
			bar.Move(fyne.NewPos(x, base-h))
			base -= h
		}
		lbl := r.xlabels[i]
		lbl.Hidden = i%every != 0
		lbl.Resize(fyne.NewSize(slot*float32(every), 14))
		lbl.Move(fyne.NewPos(chartLeft+slot*float32(i)+slot/2-slot*float32(every)/2, size.Height-16))
	}
	r.layoutHover(size, slot, plotH)
}

func (r *barChartRenderer) layoutHover(size fyne.Size, slot, plotH float32) {
	i := r.c.hover
	show := i >= 0 && i < len(r.c.tips)
	r.hoverBg.Hidden, r.tipBg.Hidden = !show, !show
	for _, t := range r.tipText {
		t.Hidden = !show
	}
	if !show {
		return
	}
	r.hoverBg.Resize(fyne.NewSize(slot, plotH+chartTop))
	r.hoverBg.Move(fyne.NewPos(chartLeft+slot*float32(i), 0))
	lines := strings.Split(r.c.tips[i], "\n")
	w := float32(0)
	for j, t := range r.tipText {
		t.Text = lines[j]
		w = max(w, t.MinSize().Width)
	}
	w += 20
	h := float32(len(r.tipText))*17 + 12
	x := chartLeft + slot*float32(i) + slot + 6
	if x+w > size.Width {
		x = chartLeft + slot*float32(i) - w - 6
	}
	r.tipBg.Resize(fyne.NewSize(w, h))
	r.tipBg.Move(fyne.NewPos(x, chartTop))
	for j, t := range r.tipText {
		t.Move(fyne.NewPos(x+10, chartTop+6+float32(j)*17))
	}
}

func (r *barChartRenderer) MinSize() fyne.Size { return r.c.MinSize() }

func (r *barChartRenderer) Refresh() {
	r.Layout(r.c.Size())
	for _, o := range r.Objects() {
		o.Refresh()
	}
}

func (r *barChartRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.hoverBg}
	for _, g := range r.grid {
		objs = append(objs, g)
	}
	for _, t := range r.axis {
		objs = append(objs, t)
	}
	for _, b := range r.bars {
		objs = append(objs, b)
	}
	for _, t := range r.xlabels {
		objs = append(objs, t)
	}
	objs = append(objs, r.tipBg)
	for _, t := range r.tipText {
		objs = append(objs, t)
	}
	return objs
}

func (r *barChartRenderer) Destroy() {}
