package main

// The shared pieces of the tab pages (Accounts, Switching, Codex, MCP, Logs)
// and the Switching, MCP and Logs pages themselves. They use the Home
// palette, the same one the panel's Home cards use.

import (
	"fmt"
	"image/color"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/clicreds"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/ratelimits"
)

// ---------- shared building blocks ----------

// pageHeader is a page's title and one-line description, with its actions on
// the right.
func pageHeader(c homeColors, title, subtitle string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	left := vbox(0, homeText(title, 20, true, c.label), flushNote(subtitle))
	if len(actions) == 0 {
		return left
	}
	right := container.New(layout.NewCustomPaddedHBoxLayout(8), actions...)
	return container.NewBorder(nil, nil, nil, container.NewVBox(layout.NewSpacer(), right), left)
}

// flushNote is a wrapped muted line, shifted left so its text lines up with
// the plain text around it (a Label insets its text).
func flushNote(text string) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, -8, 0), lowLabel(text))
}

// pageScroll stacks a page's blocks and scrolls them. It scrolls sideways too:
// a page wider than the panel (MCP's columns, long log lines) would otherwise
// grow the window, and Fyne never shrinks it back for the other tabs.
func pageScroll(items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewScroll(container.New(layout.NewCustomPaddedLayout(4, 12, 4, 4), vbox(14, items...)))
}

// vmid centres a cell vertically in its row and keeps it left-aligned.
func vmid(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}

// homeRedLink is a quiet destructive text button.
func homeRedLink(c homeColors, label string, tap func()) fyne.CanvasObject {
	return container.NewCenter(newPayButton(label, c.redText, color.Transparent, nil, c.fill2, 11.5, false, 3, 6, 10, tap))
}

// homeSelect is a capsule showing the current choice; tapping it opens the
// options in a menu below it.
func homeSelect(c homeColors, current string, options []string, enabled bool, pick func(string)) fyne.CanvasObject {
	fg := c.label
	if !enabled {
		fg = c.label2
	}
	chevron := canvas.NewImageFromResource(theme.NewThemedResource(theme.MenuDropDownIcon()))
	chevron.SetMinSize(fyne.NewSize(14, 14))
	chevron.FillMode = canvas.ImageFillContain
	var row *payInteractiveRow
	row = newPayInteractiveRow(
		container.New(layout.NewCustomPaddedHBoxLayout(4), homeText(current, 12, false, fg), container.NewCenter(chevron)),
		c.fill, c.sep, 8, 4, 10, func() {
			if !enabled {
				return
			}
			cv := fyne.CurrentApp().Driver().CanvasForObject(row)
			if cv == nil {
				return
			}
			items := make([]*fyne.MenuItem, len(options))
			for i, o := range options {
				o := o
				items[i] = fyne.NewMenuItem(o, func() { pick(o) })
				items[i].Checked = o == current
			}
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(row).Add(fyne.NewPos(0, row.Size().Height+2))
			widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), cv, pos)
		})
	return container.NewCenter(row)
}

// homeMore is the round "···" button that opens a menu of less common actions.
func homeMore(c homeColors, items ...*fyne.MenuItem) fyne.CanvasObject {
	var btn *payButton
	btn = newPayButton("···", c.label, c.fill, nil, c.sep, 13, true, 2, 8, 14, func() {
		cv := fyne.CurrentApp().Driver().CanvasForObject(btn)
		if cv == nil {
			return
		}
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(btn).Add(fyne.NewPos(0, btn.Size().Height+2))
		widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), cv, pos)
	})
	return container.NewCenter(btn)
}

// homeCheck is a small rounded checkbox in the blue accent.
type homeCheck struct {
	widget.BaseWidget
	c        homeColors
	on       bool
	disabled bool
	change   func(bool)
}

func newHomeCheck(c homeColors, on, enabled bool, change func(bool)) *homeCheck {
	h := &homeCheck{c: c, on: on, disabled: !enabled, change: change}
	h.ExtendBaseWidget(h)
	return h
}

func (h *homeCheck) MinSize() fyne.Size { return fyne.NewSize(18, 18) }

func (h *homeCheck) Tapped(*fyne.PointEvent) {
	if h.disabled {
		return
	}
	h.on = !h.on
	h.Refresh()
	if h.change != nil {
		h.change(h.on)
	}
}

func (h *homeCheck) CreateRenderer() fyne.WidgetRenderer {
	r := &homeCheckRenderer{h: h, box: canvas.NewRectangle(color.Transparent), a: canvas.NewLine(color.Transparent), b: canvas.NewLine(color.Transparent)}
	r.box.CornerRadius = 5
	r.a.StrokeWidth, r.b.StrokeWidth = 2, 2
	r.Refresh()
	return r
}

type homeCheckRenderer struct {
	h    *homeCheck
	box  *canvas.Rectangle
	a, b *canvas.Line
}

func (r *homeCheckRenderer) Layout(size fyne.Size) {
	r.box.Resize(size)
	w, h := size.Width, size.Height
	r.a.Position1, r.a.Position2 = fyne.NewPos(w*0.26, h*0.52), fyne.NewPos(w*0.43, h*0.70)
	r.b.Position1, r.b.Position2 = fyne.NewPos(w*0.43, h*0.70), fyne.NewPos(w*0.76, h*0.32)
}

func (r *homeCheckRenderer) MinSize() fyne.Size { return r.h.MinSize() }

func (r *homeCheckRenderer) Refresh() {
	c := r.h.c
	if r.h.on {
		r.box.FillColor, r.box.StrokeWidth = c.blue, 0
		r.a.StrokeColor, r.b.StrokeColor = c.onBlue, c.onBlue
	} else {
		r.box.FillColor, r.box.StrokeColor, r.box.StrokeWidth = color.Transparent, c.label2, 1.4
		r.a.StrokeColor, r.b.StrokeColor = color.Transparent, color.Transparent
	}
	r.box.Refresh()
	r.a.Refresh()
	r.b.Refresh()
}

func (r *homeCheckRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.box, r.a, r.b}
}
func (r *homeCheckRenderer) Destroy() {}

// colLayout centres one child in a column no wider than w.
type colLayout struct{ w float32 }

func (l colLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := min(l.w, size.Width)
	for _, o := range objs {
		o.Resize(fyne.NewSize(w, size.Height))
		o.Move(fyne.NewPos((size.Width-w)/2, 0))
	}
}

func (l colLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.Size{}
	}
	return fyne.NewSize(300, objs[0].MinSize().Height)
}

// prefGroup is a card of settings rows with a hairline between them.
func prefGroup(c homeColors, rows ...fyne.CanvasObject) fyne.CanvasObject {
	items := make([]fyne.CanvasObject, 0, len(rows)*2)
	for _, r := range rows {
		if r == nil {
			continue
		}
		if len(items) > 0 {
			items = append(items, homeRule(c))
		}
		items = append(items, r)
	}
	return homeCard(c, 2, 16, vbox(0, items...))
}

// prefRow is one setting: a bold title, an optional description, and the
// control on the right.
func prefRow(c homeColors, title, desc string, control fyne.CanvasObject) fyne.CanvasObject {
	left := vbox(0, homeText(title, 13, true, c.label))
	if desc != "" {
		left = vbox(0, homeText(title, 13, true, c.label), flushNote(desc))
	}
	return container.New(layout.NewCustomPaddedLayout(12, 12, 0, 0), container.NewBorder(nil, nil, nil, container.NewCenter(control), left))
}

func prefSection(c homeColors, title string) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(8, 0, 16, 0), homeText(title, 12, true, c.label2))
}

func prefToggle(c homeColors, on, enabled bool, changed func(bool)) fyne.CanvasObject {
	t := newToggleSwitch(on, changed)
	t.tint = c.green
	t.SetDisabled(!enabled)
	return t
}

// prefPage is a settings page: its header, then sections and cards in one
// centred column that scrolls.
func prefPage(header fyne.CanvasObject, blocks ...fyne.CanvasObject) fyne.CanvasObject {
	items := append([]fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(2, 6, 4, 4), header)}, blocks...)
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(4, 12, 4, 4), container.New(colLayout{w: 720}, vbox(8, items...))))
}

// prefHeader is pageHeader for the centred column: the actions sit beside
// the title, so the wrapped description below keeps the full width (beside
// buttons its height comes out wrong).
func prefHeader(c homeColors, title, subtitle string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	right := container.NewCenter(container.New(layout.NewCustomPaddedHBoxLayout(8), actions...))
	return vbox(2, container.NewBorder(nil, nil, nil, right, vmid(homeText(title, 20, true, c.label))), flushNote(subtitle))
}

// prefSectionWith is a section title with a quiet action on its right.
func prefSectionWith(c homeColors, title string, action fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, container.New(layout.NewCustomPaddedLayout(8, 0, 0, 8), action), prefSection(c, title))
}

// prefItem is one row of a list card: a bold name (with an optional green
// tag beside it) over a muted detail line, and its actions on the right.
func prefItem(c homeColors, title, tag, detail string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	name := fyne.CanvasObject(homeText(title, 13, true, c.label))
	if tag != "" {
		name = container.New(layout.NewCustomPaddedHBoxLayout(6), name, homePill(tag, c.greenText, c.greenBg))
	}
	left := name
	if detail != "" {
		left = vbox(3, name, homeText(detail, 11.5, false, c.label2))
	}
	right := container.NewCenter(container.New(layout.NewCustomPaddedHBoxLayout(6), actions...))
	return container.New(layout.NewCustomPaddedLayout(12, 12, 0, 0), container.NewBorder(nil, nil, nil, right, vmid(left)))
}

// prefEmpty is the one muted line a list card shows when it has no rows.
func prefEmpty(c homeColors, text string) fyne.CanvasObject {
	return homeCard(c, 16, 16, homeText(text, 12, false, c.label2))
}

// ---------- Switching ----------

func (a *app) manageProfilesTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	profileCount := len(a.cfg.Profiles)
	showMultiple := a.cfg.ShowMultipleProfiles
	autoSwitch := a.cfg.AutoSwitchOnLimit
	autoSwitchAt := a.cfg.AutoSwitchPercent()
	switchCLI := a.cfg.SwitchCLI
	strategy := a.cfg.AutoSwitchStrategy
	a.mu.Unlock()
	multi := profileCount > 1

	const (
		most = "Most room left"
		soon = "Weekly limit that resets soonest"
	)
	curStrategy := most
	if strategy == profile.StrategyConsumeFirst {
		curStrategy = soon
	}
	percents := []string{"100%", "95%", "90%", "80%"}

	auto := prefGroup(c,
		prefRow(c, "Switch accounts automatically", "When the active account's session fills up, move to another account without asking.",
			prefToggle(c, autoSwitch, multi, func(on bool) { a.runOp(func() error { return a.doSetAutoSwitch(on) }) })),
		prefRow(c, "Switch when session use reaches", "",
			homeSelect(c, fmt.Sprintf("%d%%", autoSwitchAt), percents, multi, func(v string) {
				a.runOp(func() error { return a.doSetAutoSwitchAt(pct(v)) })
			})),
		prefRow(c, "Switch to the account with", "Soonest reset spends weekly allowance that would otherwise expire. Accounts set to skip are never chosen.",
			homeSelect(c, curStrategy, []string{most, soon}, multi, func(v string) {
				s := profile.StrategyMostRoom
				if v == soon {
					s = profile.StrategyConsumeFirst
				}
				a.runOp(func() error { return a.doSetStrategy(s) })
			})),
	)

	blocks := []fyne.CanvasObject{
		pageHeader(c, "Switching", "How CD Switcher moves between your Claude accounts"),
		prefSection(c, "Auto-switch"), auto,
	}
	if clicreds.Supported() {
		blocks = append(blocks, prefSection(c, "When you switch"), prefGroup(c,
			prefRow(c, "Switch the Claude Code login too", "Each account keeps its own CLI login and restores it. An account never signed in to the CLI starts signed out.",
				prefToggle(c, switchCLI, true, func(on bool) { a.runOp(func() error { return a.doSetSwitchCLI(on) }) }))))
	}
	blocks = append(blocks, prefSection(c, "Menu bar and limit readings"), prefGroup(c,
		prefRow(c, "Show every account in the menu bar", "One small coloured icon per account, with the active one ringed, instead of a single app icon.",
			prefToggle(c, showMultiple, true, func(on bool) { a.runOp(func() error { return a.doSetShowMultipleProfiles(on) }) })),
		prefRow(c, "Read limits from Claude Code", "Claude Code reports limits after every reply, so meters stay current without polling. Your statusline keeps running.",
			prefToggle(c, ratelimits.Installed(), harness.Installed(harness.Claude, ""), func(on bool) { a.runOp(func() error { return a.doSetStatusline(on) }) })),
	))
	return prefPage(blocks[0], blocks[1:]...)
}

func pct(s string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(s, "%"))
	return n
}

// ---------- MCP servers ----------

func (a *app) mcpTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	servers := append([]mcp.Server(nil), a.cfg.MCPServers...)
	a.mu.Unlock()
	desktopDir := a.desktopDir()
	var tools []string
	for _, t := range mcp.Tools {
		if harness.Installed(t, desktopDir) {
			tools = append(tools, t)
		}
	}

	var templateNames []string
	for _, t := range mcp.Templates {
		templateNames = append(templateNames, t.Name)
	}
	header := pageHeader(c, "MCP servers", "One list, written into each tool's own config. Tick the tools that should get each server.",
		homeBtn(c, "Import from tools", false, func() { a.runOp(a.doMCPImport) }),
		homeSelect(c, "Templates", templateNames, true, func(name string) {
			for _, t := range mcp.Templates {
				if t.Name == name {
					a.mcpForm(t, "")
				}
			}
		}),
		homeBtn(c, "Paste JSON…", false, a.mcpPasteForm),
		homeBtn(c, "Add server", true, func() { a.mcpForm(mcp.Server{}, "") }),
	)

	widths := []float32{0}
	for range tools {
		widths = append(widths, 104)
	}
	widths = append(widths, 110)
	cols := bentoCols{widths: widths, gap: 12}

	head := []fyne.CanvasObject{homeText("Server", 12, false, c.label2)}
	for _, t := range tools {
		l := homeText(harness.Title(t), 12, false, c.label2)
		l.Alignment = fyne.TextAlignCenter
		head = append(head, container.NewCenter(l))
	}
	head = append(head, layout.NewSpacer())
	rows := []fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(10, 10, 0, 0), container.New(cols, head...))}
	if len(servers) == 0 {
		rows = append(rows, homeRule(c), container.New(layout.NewCustomPaddedLayout(14, 14, 0, 0),
			homeText("No MCP servers yet. Add one, paste JSON, or import the ones your tools already have.", 12, false, c.label2)))
	}
	for _, s := range servers {
		rows = append(rows, homeRule(c), container.New(layout.NewCustomPaddedLayout(12, 12, 0, 0), container.New(cols, a.mcpRow(c, s, tools)...)))
	}
	table := homeCard(c, 4, 22, vbox(0, rows...))
	note := flushNote("Changes reach Claude Code and Cursor at their next start, and Claude Desktop after a restart. Only tools installed on this computer get a column.")
	return pageScroll(header, table, note)
}

// mcpRow is one server's cells: name and command, a checkbox per installed
// tool, then Edit and Remove.
func (a *app) mcpRow(c homeColors, s mcp.Server, tools []string) []fyne.CanvasObject {
	what := strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	if s.Remote() {
		what = s.URL
	}
	name := fyne.CanvasObject(homeText(s.Name, 13, true, c.label))
	if s.Remote() {
		name = container.New(layout.NewCustomPaddedHBoxLayout(6), homeText(s.Name, 13, true, c.label), homePill("Remote", c.label2, c.fill))
	}
	detail := homeText(clip(what, 64), 11, false, c.label2)
	detail.TextStyle = fyne.TextStyle{Monospace: true}
	cells := []fyne.CanvasObject{vmid(vbox(3, name, detail))}
	for _, t := range tools {
		t, sname := t, s.Name
		if !mcp.Supports(t, s) {
			cells = append(cells, container.NewCenter(homeText("Not supported", 11, false, c.label2)))
			continue
		}
		cells = append(cells, container.NewCenter(newHomeCheck(c, s.Enabled[t], true, func(on bool) {
			a.runOp(func() error { return a.doMCPToggle(sname, t, on) })
		})))
	}
	srv := s
	cells = append(cells, container.NewCenter(container.New(layout.NewCustomPaddedHBoxLayout(8),
		homeLink(c, "Edit", func() { a.mcpForm(srv, srv.Name) }),
		homeRedLink(c, "Remove", func() { a.runOp(func() error { return a.doMCPDelete(srv.Name) }) }),
	)))
	return cells
}

// ---------- Logs ----------

// logTailBytes caps how much of the log the Logs page reads; the file grows
// for months and only the recent end helps when something looks wrong.
const logTailBytes = 128 << 10

// logRows caps how many lines the page draws; Copy all still copies the
// whole tail.
const logRows = 300

// logsTabContent is the "Logs" page: the newest lines of cd-switcher.log, so
// a failing usage fetch or switch is visible without opening a terminal.
func (a *app) logsTabContent() fyne.CanvasObject {
	c := homePalette()
	text := readLogTail(logPath, logTailBytes)
	header := pageHeader(c, "Logs", "Newest first. Failed usage fetches, switches and updates are recorded here.",
		homeBtn(c, "Refresh", false, func() { a.redrawSettings() }),
		homeBtn(c, "Copy all", false, func() { a.fyneApp.Clipboard().SetContent(text) }),
		homeBtn(c, "Open file", false, func() {
			if u, err := url.Parse("file://" + logPath); err == nil {
				_ = a.fyneApp.OpenURL(u)
			}
		}),
	)

	entries := parseLog(text)
	if len(entries) == 0 {
		return pageScroll(header, homeCard(c, 18, 20, homeText("Nothing logged yet.", 12, false, c.label2)))
	}
	capped := len(entries) > logRows
	if capped {
		entries = entries[:logRows]
	}
	rows := []fyne.CanvasObject{}
	day := ""
	now := time.Now()
	for _, e := range entries {
		if d := logDay(e.when, now); d != day {
			day = d
			pad := float32(8)
			if len(rows) > 0 {
				pad = 16
			}
			rows = append(rows, container.New(layout.NewCustomPaddedLayout(pad, 6, 0, 0), homeText(d, 12, true, c.label2)))
		} else {
			rows = append(rows, homeRule(c))
		}
		rows = append(rows, container.New(layout.NewCustomPaddedLayout(7, 7, 0, 0), logRow(c, e)))
	}
	items := []fyne.CanvasObject{header, homeCard(c, 6, 20, vbox(0, rows...))}
	if capped {
		items = append(items, flushNote("Showing the newest "+strconv.Itoa(logRows)+" lines. Copy all copies everything read from the file."))
	}
	return pageScroll(items...)
}

type logEntry struct {
	when time.Time // zero for a line without a timestamp
	msg  string
}

// parseLog reads newest-first log text ("2006/01/02 15:04:05 message" per
// line) into entries. A line without a timestamp belongs to the message above
// it in the file, i.e. the next timed line in this order.
func parseLog(text string) []logEntry {
	var out []logEntry
	var tail []string // untimed lines seen since the last timed one
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l) > 20 {
			if t, err := time.ParseInLocation("2006/01/02 15:04:05", l[:19], time.Local); err == nil {
				msg := strings.TrimSpace(l[19:])
				// The text is newest first, so a message's continuation lines
				// came before its timestamped first line.
				for i := len(tail) - 1; i >= 0; i-- {
					msg += "\n" + tail[i]
				}
				tail = tail[:0]
				out = append(out, logEntry{when: t, msg: msg})
				continue
			}
		}
		tail = append(tail, l)
	}
	for i := len(tail) - 1; i >= 0; i-- { // cut off from its timestamp
		out = append(out, logEntry{msg: tail[i]})
	}
	return out
}

func logDay(t, now time.Time) string {
	if t.IsZero() {
		return "Earlier"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case !t.Before(today):
		return "Today"
	case !t.Before(today.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return t.Format("Mon, Jan 2")
}

func logRow(c homeColors, e logEntry) fyne.CanvasObject {
	when := ""
	if !e.when.IsZero() {
		when = e.when.Format("3:04 PM")
	}
	tm := homeText(when, 12, false, c.label2)
	tm.TextStyle = fyne.TextStyle{Monospace: true}

	style := widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}, SizeName: theme.SizeNameText, ColorName: theme.ColorNameForeground}
	if low := strings.ToLower(e.msg); strings.Contains(low, "failed") || strings.Contains(low, "error") {
		style.ColorName = theme.ColorNameError // no bold: the monospace face has no bold variant
	}
	msg := widget.NewRichText(&widget.TextSegment{Text: e.msg, Style: style})
	msg.Wrapping = fyne.TextWrapWord
	return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(78, 24), container.New(layout.NewCustomPaddedLayout(5, 0, 0, 0), tm)), nil, msg)
}
