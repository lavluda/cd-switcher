package main

// The Usage page: tokens and cost from the Claude Code and Codex session logs
// on this machine, and from Cursor's account usage, by hour or day, model and project.

import (
	"context"
	"errors"
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

	"github.com/lavluda/cd-switcher/internal/cursorusage"
	"github.com/lavluda/cd-switcher/internal/opencodeusage"
	"github.com/lavluda/cd-switcher/internal/sessionlog"
	"github.com/lavluda/cd-switcher/internal/uitheme"
)

// usageState is the Usage page's data and choices. UI-thread only, except
// scanning, which guards the background scan.
type usageState struct {
	scanner       *sessionlog.Scanner
	scannedAt     time.Time
	scanning      atomic.Bool
	rangeIdx      int // 0 today, 1 last 7 days, 2 last 30 days
	toolIdx       int // 0 all, 1 Claude Code, 2 Codex
	metric        int // what the chart shows: 0 cost, 1 requests, 2 tokens
	table         int // 0 by model, 1 by project
	cursorAt      time.Time
	cursorKnown   bool
	cursorPercent float64
	cursorResets  time.Time
	opencodeAt    time.Time
}

// codexColor marks Codex in the chart and tables; Claude Code uses the
// app's accent, so each tool keeps one color everywhere on the page.
var codexColor = color.NRGBA{R: 0x2B, G: 0x7A, B: 0x8C, A: 0xFF}

// cursorColor marks Cursor in the chart and tables.
var cursorColor = color.NRGBA{R: 0x6E, G: 0x56, B: 0xCF, A: 0xFF}

// opencodeColor marks OpenCode in the chart and tables.
var opencodeColor = color.NRGBA{R: 0xC4, G: 0xA3, B: 0x5A, A: 0xFF}

func toolColor(tool string) color.Color {
	switch tool {
	case sessionlog.Claude:
		return homePalette().blue
	case sessionlog.Codex:
		return codexColor
	case sessionlog.Cursor:
		return cursorColor
	case sessionlog.OpenCode:
		return opencodeColor
	}
	return uitheme.CurrentMuted()
}

var usageTools = []string{"", sessionlog.Claude, sessionlog.Codex, sessionlog.Cursor, sessionlog.OpenCode}

var usageNames = []string{"Claude Code", "Codex", "Cursor", "OpenCode"}

// redrawSettings rebuilds the open page after a choice on it.
func (a *app) redrawSettings() {
	if a.panelShowing() {
		a.redrawPanel()
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
		a.scanCursor(u)
		a.scanOpenCode(u)
		fyne.Do(func() {
			u.scannedAt = time.Now()
			a.redrawSettings()
		})
	}()
}

// scanCursor fills the Usage page's Cursor rows from the signed-in account.
// Cursor keeps no local token log. A successful read is reused for five minutes.
func (a *app) scanCursor(u *usageState) {
	if !u.cursorAt.IsZero() && time.Since(u.cursorAt) < 5*time.Minute {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	period, perr := cursorusage.Current(ctx)
	events, err := cursorusage.Events(ctx, time.Now().Add(-sessionlog.Window), time.Now())
	if perr != nil && !errors.Is(perr, cursorusage.ErrNoLogin) {
		logf("cursor usage: %v", perr)
	}
	if err != nil && !errors.Is(err, cursorusage.ErrNoLogin) {
		logf("cursor usage history: %v", err)
	}
	u.cursorAt = time.Now()
	if err == nil {
		rows := make([]sessionlog.External, 0, len(events))
		for _, e := range events {
			rows = append(rows, sessionlog.External{
				At: e.At, Model: e.Model,
				Usage: sessionlog.Usage{
					Requests: 1, Input: e.Input, Output: e.Output,
					CacheRead: e.CacheRead, CacheWrite: e.CacheWrite, Cost: e.Cost,
				},
			})
		}
		u.scanner.Replace(sessionlog.Cursor, rows)
	}
	if perr == nil {
		u.cursorKnown = true
		u.cursorPercent = period.Percent
		u.cursorResets = period.Resets
	}
}

// scanOpenCode fills the Usage page from OpenCode's local database.
func (a *app) scanOpenCode(u *usageState) {
	if !u.opencodeAt.IsZero() && time.Since(u.opencodeAt) < 5*time.Minute {
		return
	}
	rows, err := opencodeusage.Read(time.Now().Add(-sessionlog.Window))
	u.opencodeAt = time.Now()
	if err != nil {
		logf("opencode usage: %v", err)
		return
	}
	ext := make([]sessionlog.External, 0, len(rows))
	for _, r := range rows {
		ext = append(ext, sessionlog.External{
			At: r.At, Model: r.Model, Project: r.Project,
			Usage: sessionlog.Usage{
				Requests: 1, Input: r.Input, Output: r.Output,
				CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, Cost: r.Cost,
			},
		})
	}
	u.scanner.Replace(sessionlog.OpenCode, ext)
}

func (a *app) usageTabContent() fyne.CanvasObject {
	c := homePalette()
	u := &a.usage
	if u.scanner == nil {
		u.scanner = sessionlog.NewScanner()
	}
	if time.Since(u.scannedAt) > time.Minute {
		a.scanUsage()
	}

	status := "Reading logs…"
	if !u.scannedAt.IsZero() {
		status = "Updated " + u.scannedAt.Format("3:04 PM")
	}
	refresh := homeBtn(c, "↻", false, func() {
		u.scannedAt = time.Time{}
		u.cursorAt = time.Time{}
		u.opencodeAt = time.Time{}
		a.scanUsage()
	})
	header := container.NewBorder(nil, nil,
		vbox(2, homeText("Usage", 20, true, c.label), homeText("Tokens and estimated cost, priced at API list rates", 12, false, c.label2)),
		container.New(layout.NewCustomPaddedHBoxLayout(8), container.NewCenter(homeText(status, 11.5, false, c.label2)), refresh))
	note := lowLabel("Claude Code and Codex come from the session logs on this Mac. Cursor comes from the usage its signed-in account reports. OpenCode comes from its local database.")

	ranges := homeSegmented(c, []string{"Today", "7 days", "30 days"}, u.rangeIdx, 12, 11, func(i int) { u.rangeIdx = i; a.redrawSettings() })
	tools := homeSegmented(c, append([]string{"All tools"}, usageNames...), u.toolIdx, 12, 11, func(i int) { u.toolIdx = i; a.redrawSettings() })
	filters := container.NewBorder(nil, nil, ranges, tools)

	if u.scannedAt.IsZero() {
		return scrollTab(header, filters, homeCard(c, 18, 18, homeText("Reading session logs. The first read takes a few seconds.", 12, false, c.label2)), note)
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
		msg := "No sessions in this range."
		if usageTools[u.toolIdx] == sessionlog.Cursor {
			msg = "Cursor reports included usage as a weekly percent. It does not send an hourly token log for this account, so there is no chart."
		}
		return scrollTab(withoutNil(header, filters, a.cursorUsageLine(), homeCard(c, 18, 18, homeText(msg, 12, false, c.label2)), note)...)
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
	for i, name := range usageNames {
		if u.toolIdx == 0 || u.toolIdx == i+1 {
			legend.Add(legendItem(name, usageTools[i+1]))
		}
	}
	chartHead := container.NewBorder(nil, nil, homeText(chartTitle, 13, true, c.label), legend)
	overview := homeCard(c, 12, 14, vbox(10, tiles, homeRule(c), chartHead, chart))

	tabs := homeSegmented(c, []string{"By model", "By project"}, u.table, 12, 11, func(i int) { u.table = i; a.redrawSettings() })
	rows := rep.ByModel
	first := "Model"
	if u.table == 1 {
		rows, first = rep.ByProject, "Project"
	}
	table := homeCard(c, 12, 14, vbox(8, container.NewBorder(nil, nil, homeText("Breakdown", 13, true, c.label), tabs), usageTable(first, rows, tot)))

	items := withoutNil(header, filters, a.cursorUsageLine(), overview, table)
	if len(rep.Unpriced) > 0 {
		names := make([]string, len(rep.Unpriced))
		for i, m := range rep.Unpriced {
			names[i] = modelName(m)
		}
		items = append(items, lowLabel("No list price is known for "+strings.Join(names, ", ")+", so their tokens are counted but not priced."))
	}
	return scrollTab(append(items, note)...)
}

// withoutNil drops the nil entries, so an optional line leaves no gap.
func withoutNil(objs ...fyne.CanvasObject) []fyne.CanvasObject {
	out := objs[:0]
	for _, o := range objs {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// cursorUsageLine is the included-usage percent Cursor reports for this cycle.
func (a *app) cursorUsageLine() fyne.CanvasObject {
	u := &a.usage
	if !u.cursorKnown {
		return nil
	}
	when := "soon"
	if !u.cursorResets.IsZero() {
		when = u.cursorResets.Local().Format("Mon 3:04pm")
	}
	return lowLabel(fmt.Sprintf("Cursor included usage %.0f%% · resets %s", u.cursorPercent, when))
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
	return vbox(0, objs...)
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
	r := &statTileRenderer{t: t,
		bg:    canvas.NewRectangle(color.Transparent),
		label: canvas.NewText(t.label, nil),
		value: canvas.NewText(t.value, nil),
	}
	r.bg.CornerRadius = 12
	r.label.TextSize = 12
	r.value.TextSize = 24
	r.value.TextStyle = fyne.TextStyle{Bold: true}
	r.Refresh()
	return r
}

// statTileRenderer paints a tile; the one being charted sits on a soft fill.
type statTileRenderer struct {
	t            *statTile
	bg           *canvas.Rectangle
	label, value *canvas.Text
}

func (r *statTileRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.label.Move(fyne.NewPos(12, 10))
	r.value.Move(fyne.NewPos(12, 10+r.label.MinSize().Height+2))
}

func (r *statTileRenderer) MinSize() fyne.Size {
	return fyne.NewSize(max(r.label.MinSize().Width, r.value.MinSize().Width)+24, r.label.MinSize().Height+r.value.MinSize().Height+24)
}

func (r *statTileRenderer) Refresh() {
	c := homePalette()
	r.bg.FillColor, r.label.Color = color.Transparent, c.label2
	if r.t.selected {
		r.bg.FillColor, r.label.Color = c.fill2, c.blueText
		r.label.TextStyle.Bold = true
	}
	r.value.Color = c.label
	r.bg.Refresh()
	r.label.Refresh()
	r.value.Refresh()
}

func (r *statTileRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.label, r.value}
}

func (r *statTileRenderer) Destroy() {}

// ---------- bar chart ----------

// barChart draws one rounded bar per hour or day, stacked by tool, over a
// faint grid. Hovering a bar dims the others and names its figures in a small card.
type barChart struct {
	widget.BaseWidget
	labels []string
	values [][]float64 // per bucket, one figure per tool in usageNames
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
		seg := make([]float64, len(usageNames))
		label := b.Start.Format("15:04")
		when := b.Start.Format("15:04") + "–" + b.Start.Add(time.Hour).Format("15:04")
		if !rep.Hourly {
			label = b.Start.Format("Jan 2")
			when = b.Start.Format("Mon, Jan 2")
		}
		tip := when
		for i, name := range usageNames {
			seg[i] = pick(b.Tools[usageTools[i+1]])
			tip += "\n" + name + "  " + format(seg[i])
		}
		c.labels = append(c.labels, label)
		c.values = append(c.values, seg)
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
	chartLeft   = 40
	chartBottom = 22
	chartTop    = 10
	// barGap separates the stacked tool segments; barRadius rounds each one.
	barGap    = 2
	barRadius = 5
	// dimmed is the opacity of the bars that are not hovered.
	dimmed = 0.3
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
	nSeg := len(usageNames)
	peak := 0.0
	for _, v := range c.values {
		sum := 0.0
		for _, x := range v {
			sum += x
		}
		peak = max(peak, sum)
	}
	r.top = niceMax(peak)
	for i := 0; i < 3; i++ {
		// The baseline stays solid; the levels above it are fainter.
		line := canvas.NewRectangle(fadeColor(uitheme.CurrentGridLine(), []float32{1, 0.6, 0.6}[i]))
		r.grid = append(r.grid, line)
		label := newChartText(c.format(r.top*float64(i)/2), fyne.TextAlignTrailing)
		label.Hidden = i == 0 // the baseline is zero; the label only adds noise
		r.axis = append(r.axis, label)
	}
	for i, v := range c.values {
		for s := 0; s < nSeg; s++ {
			bar := canvas.NewRectangle(toolColor(usageTools[s+1]))
			if s >= len(v) || v[s] == 0 {
				bar.Hide()
			}
			r.bars = append(r.bars, bar)
		}
		r.xlabels = append(r.xlabels, newChartText(c.labels[i], fyne.TextAlignCenter))
	}
	r.tipBg = canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	r.tipBg.CornerRadius = 8
	r.tipBg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	r.tipBg.StrokeWidth = 1
	for i := 0; i < 1+nSeg; i++ {
		t := canvas.NewText("", uitheme.CurrentForeground())
		t.TextSize = 12
		r.tipText = append(r.tipText, t)
	}
	r.tipText[0].TextStyle = fyne.TextStyle{Bold: true}
	for s := 0; s < nSeg; s++ {
		r.tipDots = append(r.tipDots, canvas.NewCircle(toolColor(usageTools[s+1])))
	}
	return r
}

type barChartRenderer struct {
	c       *barChart
	top     float64
	grid    []*canvas.Rectangle
	axis    []*canvas.Text
	bars    []*canvas.Rectangle
	xlabels []*canvas.Text
	tipBg   *canvas.Rectangle
	tipText []*canvas.Text
	tipDots []*canvas.Circle // colour keys for the tool lines of the card
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
	barW := max(min(slot*0.7, 34), 2)
	// Label every k-th bar so labels never overlap.
	every := int(math.Ceil(float64(52 / slot)))
	nSeg := len(usageNames)
	for i, v := range r.c.values {
		x := chartLeft + slot*float32(i) + (slot-barW)/2
		base := chartTop + plotH
		for s := 0; s < nSeg; s++ {
			val := 0.0
			if s < len(v) {
				val = v[s]
			}
			h := float32(val/r.top) * plotH
			if val > 0 && h < 2 {
				h = 2
			}
			bar := r.bars[i*nSeg+s]
			bar.CornerRadius = min(barRadius, barW/2, h/2)
			bar.FillColor = toolColor(usageTools[s+1])
			if r.c.hover >= 0 && r.c.hover != i {
				bar.FillColor = fadeColor(bar.FillColor, dimmed)
			}
			bar.Resize(fyne.NewSize(barW, h))
			bar.Move(fyne.NewPos(x, base-h))
			base -= h
			if h > 0 {
				base -= barGap // keeps the next segment clear of this one
			}
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
	r.tipBg.Hidden = !show
	for _, t := range r.tipText {
		t.Hidden = !show
	}
	for _, d := range r.tipDots {
		d.Hidden = !show
	}
	if !show {
		return
	}
	lines := strings.Split(r.c.tips[i], "\n")
	w := float32(0)
	for j, t := range r.tipText {
		if j < len(lines) {
			t.Text = lines[j]
		}
		indent := float32(0)
		if j > 0 {
			indent = tipDotIndent
		}
		w = max(w, t.MinSize().Width+indent)
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
		indent := float32(0)
		if j > 0 {
			indent = tipDotIndent
			d := r.tipDots[j-1]
			d.Resize(fyne.NewSize(8, 8))
			d.Move(fyne.NewPos(x+10, chartTop+6+float32(j)*17+5))
		}
		t.Move(fyne.NewPos(x+10+indent, chartTop+6+float32(j)*17))
	}
}

// tipDotIndent leaves room for the colour dot before a tool's line in the card.
const tipDotIndent = 14

func (r *barChartRenderer) MinSize() fyne.Size { return r.c.MinSize() }

func (r *barChartRenderer) Refresh() {
	r.Layout(r.c.Size())
	for _, o := range r.Objects() {
		o.Refresh()
	}
}

func (r *barChartRenderer) Objects() []fyne.CanvasObject {
	var objs []fyne.CanvasObject
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
	for _, d := range r.tipDots {
		objs = append(objs, d)
	}
	for _, t := range r.tipText {
		objs = append(objs, t)
	}
	return objs
}

func (r *barChartRenderer) Destroy() {}

func newChartText(text string, align fyne.TextAlign) *canvas.Text {
	t := canvas.NewText(text, uitheme.CurrentMuted())
	t.TextSize = chartLabelSz
	t.Alignment = align
	return t
}

const chartLabelSz = 10
