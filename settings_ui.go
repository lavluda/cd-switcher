package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/clicreds"
	"github.com/lavluda/cd-switcher/internal/icon"
	"github.com/lavluda/cd-switcher/internal/login"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/ratelimits"
	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// settingsPage is one entry in the Settings sidebar. section starts a new
// captioned group in the sidebar when it differs from the page before.
type settingsPage struct {
	section, title string
	icon           fyne.Resource
	build          func(*app) fyne.CanvasObject
}

// A function, not a var: the page builders reach settingsContent, which
// reads this list, and Go rejects that as an initialization cycle.
func settingsPages() []settingsPage {
	return []settingsPage{
		{"", "General", theme.SettingsIcon(), (*app).generalTabContent},
		{"", "Usage", chartIcon, (*app).usageTabContent},
		{"Claude", "Accounts", theme.AccountIcon(), (*app).accountsTabContent},
		{"Claude", "Switching", theme.ViewRefreshIcon(), (*app).manageProfilesTabContent},
		{"Other tools", "Codex & Copilot", theme.ComputerIcon(), (*app).codexTabContent},
		{"Other tools", "Providers", theme.StorageIcon(), (*app).providersTabContent},
		{"Shared setup", "MCP servers", theme.GridIcon(), (*app).mcpTabContent},
		{"Shared setup", "Skills", theme.FolderIcon(), (*app).skillsTabContent},
		{"Shared setup", "Instructions", theme.DocumentIcon(), (*app).instructionsTabContent},
	}
}

// settingsContent builds the settings window: a grouped sidebar and the
// selected page. Runs on the UI thread. It is rebuilt on every refresh() (a
// stats poll, a switch, a toggle flip), so only the open page is built and
// the selection lives in a.settingsTabIndex.
//
// Update checking lives in the tray menu, not here — see
// (*app).updateMenuItem in main.go — so it works without opening this window.
func (a *app) settingsContent() fyne.CanvasObject {
	pages := settingsPages()
	if a.settingsTabIndex < 0 || a.settingsTabIndex >= len(pages) {
		a.settingsTabIndex = 0
	}
	nav := []fyne.CanvasObject{}
	section := ""
	for i, pg := range pages {
		if pg.section != section {
			section = pg.section
			nav = append(nav, navCaption(section))
		}
		i := i
		b := widget.NewButtonWithIcon(pg.title, pg.icon, func() {
			a.settingsTabIndex = i
			if a.settingsWin != nil {
				a.settingsWin.SetContent(a.settingsContent())
			}
		})
		b.Alignment = widget.ButtonAlignLeading
		b.Importance = widget.LowImportance
		if i == a.settingsTabIndex {
			b.Importance = widget.MediumImportance
		}
		if pg.title == "Usage" {
			nav = append(nav, a.usageNavItem(b))
			continue
		}
		nav = append(nav, b)
	}
	sidebar := container.New(layout.NewCustomPaddedLayout(10, 10, 8, 8), vbox(2, nav...))
	page := pages[a.settingsTabIndex].build(a)
	return container.NewBorder(nil, nil, container.NewHBox(sidebar, widget.NewSeparator()), nil, page)
}

// generalTabContent is the "General" tab: how accounts switch, when to warn
// about limits, whether CD-Switcher opens at login, and whether it checks for
// its own updates in the background. The check itself (and the menu item
// showing the result) lives in the tray menu — see (*app).updateMenuItem in
// main.go — so it works without opening this window.
func (a *app) generalTabContent() fyne.CanvasObject {
	a.mu.Lock()
	parallel := a.cfg.IsParallel()
	launchAtLogin := a.cfg.LaunchAtLogin
	warnAt := a.cfg.WarnPercent()
	autoCheckUpdates := a.cfg.AutoCheckUpdatesEnabled()
	term := a.cfg.Terminal
	a.mu.Unlock()

	rows := []fyne.CanvasObject{a.modeRow(parallel), a.alertRow(warnAt)}
	if row := a.loginRow(launchAtLogin); row != nil {
		rows = append(rows, row)
	}
	rows = append(rows, toggleRow(
		"Automatically check for updates",
		"Check once a day in the background. Check for Updates in the tray menu runs one on demand.",
		autoCheckUpdates, true,
		func(checked bool) { a.runOp(func() error { return a.doSetAutoCheckUpdates(checked) }) },
	))
	rows = append(rows, a.terminalRow(term), a.exportRow())

	return scrollTab(tabHeader("General", "Account switching, limit alerts, and startup"), group(rows...))
}

// manageProfilesTabContent is the "Switching" page: the tray-display and
// auto-switch toggles. The accounts themselves live in their own "Accounts"
// tab, since that list can get long and scrolls independently.
func (a *app) manageProfilesTabContent() fyne.CanvasObject {
	a.mu.Lock()
	profileCount := len(a.cfg.Profiles)
	showMultiple := a.cfg.ShowMultipleProfiles
	autoSwitch := a.cfg.AutoSwitchOnLimit
	autoSwitchAt := a.cfg.AutoSwitchPercent()
	switchCLI := a.cfg.SwitchCLI
	strategy := a.cfg.AutoSwitchStrategy
	a.mu.Unlock()

	rows := []fyne.CanvasObject{a.multiProfileRow(showMultiple), a.autoSwitchRow(autoSwitch, autoSwitchAt, profileCount > 1), a.strategyRow(strategy, profileCount > 1)}
	if clicreds.Supported() {
		rows = append(rows, toggleRow(
			"Switch Claude Code login too",
			"Each account remembers its own Claude Code (CLI) login and restores it when you switch. An account you haven't signed in to in the CLI starts signed out.",
			switchCLI, true,
			func(checked bool) { a.runOp(func() error { return a.doSetSwitchCLI(checked) }) },
		))
	}
	rows = append(rows, a.statuslineRow(ratelimits.Installed()))
	return scrollTab(tabHeader("Switching", "How CD-Switcher moves between your Claude accounts"), group(rows...))
}

// accountsTabContent is the "Accounts" tab: every captured Claude account
// with its live plan meters, usage history, and the switch/rename/remove
// actions.
func (a *app) accountsTabContent() fyne.CanvasObject {
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	activeID := a.cfg.ActiveProfile
	parallel := a.cfg.IsParallel()
	a.mu.Unlock()
	_, best, hasBest := a.freest(profiles)

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { go a.refreshStats(0) })
	refreshBtn.Importance = widget.LowImportance
	header := tabHeader("Accounts", "Your Claude accounts, their limits and recent use", refreshBtn)

	var cardObjs []fyne.CanvasObject
	if len(profiles) == 0 {
		cardObjs = append(cardObjs, newPanel(widget.NewLabel("No accounts yet. Use Add account… from the menu.")))
	}
	for i, p := range profiles {
		cardObjs = append(cardObjs, a.accountCard(p, p.ColorIndex(i), p.ID == activeID, parallel, hasBest && p.ID == best.ID, i == 0, i == len(profiles)-1))
	}
	cards := vbox(8, cardObjs...)

	note := widget.NewLabel("Session and weekly figures are how full each limit is. Extra usage is spend past the plan. Token counts appear when Claude's usage response includes them.")
	note.Wrapping = fyne.TextWrapWord
	note.Importance = widget.LowImportance

	return scrollTab(header, cards, note)
}

// modeRow is one row of the General group: a compact segmented control
// choosing between switch-accounts and side-by-side, plus a usage hint.
func (a *app) modeRow(parallel bool) fyne.CanvasObject {
	selected := 0
	if parallel {
		selected = 1
	}
	seg := newSegmentedControl([]string{"Switch accounts", "Side by side"}, selected, func(i int) {
		mode := profile.ModeSwitch
		if i == 1 {
			mode = profile.ModeParallel
		}
		a.runOp(func() error { return a.doSetMode(mode) })
	})
	hint := widget.NewLabel("Control-Option-S opens the next account. Control-Option-1 to 9 opens that account.")
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	return vbox(6, seg, hint)
}

// alertRow is one row of the General group: the session/weekly limit
// notification threshold.
func (a *app) alertRow(current int) fyne.CanvasObject {
	options := []string{"Off", "70%", "80%", "90%"}
	selected := "Off"
	switch current {
	case 70:
		selected = "70%"
	case 80:
		selected = "80%"
	case 90:
		selected = "90%"
	}
	var suppress bool
	sel := widget.NewSelect(options, func(value string) {
		if suppress {
			return
		}
		next := 0
		switch value {
		case "70%":
			next = 70
		case "80%":
			next = 80
		case "90%":
			next = 90
		}
		a.runOp(func() error { return a.doSetWarn(next) })
	})
	suppress = true
	sel.SetSelected(selected)
	suppress = false
	hint := widget.NewLabel("Notify once when a session or weekly limit reaches this.")
	hint.Importance = widget.LowImportance
	label := widget.NewLabelWithStyle("Limit alert", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return vbox(6, container.NewBorder(nil, nil, label, sel), hint)
}

// loginRow is the General group's launch-at-login toggle. Returns nil on a
// platform where login.Supported() is false, so the caller can drop it from
// the group entirely instead of showing a dead control.
func (a *app) loginRow(on bool) fyne.CanvasObject {
	if !login.Supported() {
		return nil
	}
	return toggleRow("Open CD-Switcher at login", "", on, true, func(checked bool) {
		a.runOp(func() error { return a.doSetLogin(checked) })
	})
}

// multiProfileRow is the Manage Profiles group's first row: a toggle that
// swaps the single tray icon for a compact swatch strip, one per profile,
// with the active one ringed.
func (a *app) multiProfileRow(on bool) fyne.CanvasObject {
	return toggleRow(
		"Show Multiple Profiles",
		"Display selected profiles as compact icons side-by-side in the menu bar, instead of a single app icon.",
		on, true,
		func(checked bool) { a.runOp(func() error { return a.doSetShowMultipleProfiles(checked) }) },
	)
}

// autoSwitchRow is the Manage Profiles group's second row: a toggle that,
// once the active profile hits 100% session usage, switches to whichever
// other profile has the most room left. Disabled with fewer than two
// profiles, since there is nothing to switch to yet.
func (a *app) autoSwitchRow(on bool, at int, enabled bool) fyne.CanvasObject {
	toggle := toggleRow(
		"Enable Auto-Switch",
		"When the active profile's session usage reaches this, automatically switch to the available profile with the most room.",
		on, enabled,
		func(checked bool) { a.runOp(func() error { return a.doSetAutoSwitch(checked) }) },
	)
	var suppress bool
	sel := widget.NewSelect([]string{"100%", "95%", "90%", "80%"}, func(value string) {
		if suppress {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSuffix(value, "%"))
		a.runOp(func() error { return a.doSetAutoSwitchAt(n) })
	})
	suppress = true
	sel.SetSelected(fmt.Sprintf("%d%%", at))
	suppress = false
	if !enabled {
		sel.Disable()
	}
	label := widget.NewLabel("Switch at")
	return vbox(6, toggle, container.NewBorder(nil, nil, label, sel))
}

// toggleRow is one titled pill-switch preference, with an optional
// description line beneath it — plain content meant to sit inside a group(),
// not its own card.
func toggleRow(title, desc string, on, enabled bool, changed func(bool)) fyne.CanvasObject {
	sw := newToggleSwitch(on, changed)
	sw.SetDisabled(!enabled)

	label := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	row := container.NewBorder(nil, nil, label, container.NewCenter(sw))

	if desc == "" {
		return row
	}
	d := widget.NewLabel(desc)
	d.Wrapping = fyne.TextWrapWord
	d.Importance = widget.LowImportance
	return vbox(4, row, d)
}

// group wraps a set of rows in a single card, one hairline divider between
// each — the grouped-list look (one logical section, one outline) instead of
// a separate floating card per row.
func group(rows ...fyne.CanvasObject) fyne.CanvasObject {
	items := make([]fyne.CanvasObject, 0, len(rows)*2)
	for _, r := range rows {
		if r == nil {
			continue
		}
		if len(items) > 0 {
			items = append(items, divider())
		}
		items = append(items, rowPadding(r))
	}
	// Zero gap: rowPadding already supplies the space around each row, so an
	// extra inter-child gap here would double it up.
	return newPanel(vbox(0, items...))
}

// rowPadding adds vertical breathing room around one row inside a group,
// without the horizontal padding newPanel's outer padding already supplies.
func rowPadding(inner fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), inner)
}

// divider is a hairline rule between rows inside the same group card.
func divider() fyne.CanvasObject {
	r := canvas.NewRectangle(uitheme.CurrentGridLine())
	r.SetMinSize(fyne.NewSize(0, 1))
	return r
}

// sectionCaption heads a group of rows on a page, inset to line up with the
// text inside the cards below it.
func sectionCaption(text string) fyne.CanvasObject {
	t := canvas.NewText(text, uitheme.CurrentForeground())
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = 14
	return container.New(layout.NewCustomPaddedLayout(8, 0, theme.InnerPadding(), 0), t)
}

// navCaption names a group of pages in the Settings sidebar.
func navCaption(text string) fyne.CanvasObject {
	t := canvas.NewText(text, uitheme.CurrentMuted())
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = 12
	return container.New(layout.NewCustomPaddedLayout(10, 0, 8, 0), t)
}

// accountCard is one account: name and plan, limit meters, spend, the recent
// session chart, and the account actions.
func (a *app) accountCard(p profile.Profile, colorIdx int, active, parallel, mostRoom, first, last bool) fyne.CanvasObject {
	id := p.ID
	windowOpen := false
	if parallel {
		dir, _ := filepath.Abs(a.store.ProfileDir(id))
		if dirs, err := claude.RunningDataDirs(); err == nil {
			for _, d := range dirs {
				if d == filepath.Clean(dir) {
					windowOpen = true
				}
			}
		}
	}

	// The name and its badges share one line; plan details go under it.
	head := container.NewHBox(widget.NewLabelWithStyle(p.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	if parallel && windowOpen {
		head.Add(badge("Open"))
	} else if !parallel && active {
		head.Add(badge("Active"))
	}
	if mostRoom {
		head.Add(badge("Most room"))
	}

	a.statsMu.Lock()
	e, ok := a.statsCache[id]
	a.statsMu.Unlock()

	body := []fyne.CanvasObject{head}
	if ok && e.plan != "" {
		plan := e.plan
		if e.tier != "" {
			plan += " · " + e.tier
		}
		if e.email != "" && !strings.EqualFold(e.email, p.Label) {
			plan = e.email + " · " + plan
		}
		body = append(body, lowLabel(plan))
	}

	switch {
	case ok && !e.fetchedAt.IsZero():
		status := widget.NewLabel("Updated " + e.fetchedAt.Local().Format("3:04pm"))
		if e.err != nil {
			status.SetText("Last reading " + e.fetchedAt.Local().Format("3:04pm"))
		}
		if e.fromClaudeCode {
			status.SetText(status.Text + " · from Claude Code")
		}
		status.Importance = widget.LowImportance
		body = append(body, status)

		meters := e.stats.Meters()
		cells := make([]fyne.CanvasObject, 0, len(meters))
		for _, m := range meters {
			cells = append(cells, usageMeter(m))
		}
		if len(cells) > 0 {
			body = append(body, container.NewGridWithColumns(2, cells...))
		}
		if detail := e.stats.Tokens.Detail(); detail != "" {
			tok := widget.NewLabel("Tokens  " + detail)
			body = append(body, tok)
		}
		if spend := e.stats.ExtraUsage.SpendLabel(); spend != "" {
			line := widget.NewLabel(spend)
			body = append(body, line)
		}
	default:
		waiting := widget.NewLabel("Usage will appear after the next refresh.")
		waiting.Importance = widget.LowImportance
		body = append(body, waiting)
	}

	history, err := usage.LoadHistory(a.historyPath(id))
	if err != nil {
		logf("stats: load history failed for %q: %v", id, err)
	}
	if len(history) >= 2 {
		chartLabel := widget.NewLabel("Session, last day")
		chartLabel.Importance = widget.LowImportance
		// The chart carries its own axis labels, so the time range is drawn
		// inside the plot frame rather than as a row beneath it.
		body = append(body, chartLabel, newSparkline(history))
	}

	if d, err := usage.LoadDaily(a.store.DailyPath(id)); err == nil {
		if sum, days := d.Summary(time.Now(), 7); days > 0 {
			week := widget.NewLabel(fmt.Sprintf("Last 7 days · peak %.0f%% session, %.0f%% weekly · limit hit %d×", sum.PeakSession, sum.PeakWeek, sum.Hits))
			week.Importance = widget.LowImportance
			body = append(body, week)
		}
	}

	skip := widget.NewCheck("Skip in auto-switch", func(on bool) {
		a.runOp(func() error { return a.doSetNoAutoSwitch(id, on) })
	})
	skip.Checked = p.NoAutoSwitch
	body = append(body, skip, a.accountActions(id, colorIdx, active, parallel, windowOpen, first, last))
	return newPanel(vbox(6, body...))
}

func (a *app) accountActions(id string, colorIdx int, active, parallel, windowOpen, first, last bool) fyne.CanvasObject {
	var primary *widget.Button
	if parallel {
		if windowOpen {
			primary = widget.NewButton("Close", func() {
				a.runOp(func() error { return a.doCloseParallel(id) })
			})
		} else {
			primary = widget.NewButton("Open", func() {
				a.runOp(func() error { return a.doOpenParallel(id) })
			})
			primary.Importance = widget.HighImportance
		}
	} else {
		primary = widget.NewButton("Switch", func() {
			a.runOp(func() error { return a.doSwitch(id) })
		})
		primary.Importance = widget.HighImportance
	}

	upBtn := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		a.runOp(func() error { return a.doMove(id, -1) })
	})
	upBtn.Importance = widget.LowImportance
	if first {
		upBtn.Disable()
	}
	downBtn := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
		a.runOp(func() error { return a.doMove(id, 1) })
	})
	downBtn.Importance = widget.LowImportance
	if last {
		downBtn.Disable()
	}
	colorBtn := widget.NewButtonWithIcon("", fyne.NewStaticResource(fmt.Sprintf("swatch%d.png", colorIdx), icon.Swatch(colorIdx)), func() {
		a.runOp(func() error { return a.doCycleColor(id) })
	})
	colorBtn.Importance = widget.LowImportance
	renameBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		a.runOp(func() error { return a.doRename(id) })
	})
	renameBtn.Importance = widget.LowImportance
	removeBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		a.runOp(func() error { return a.doRemove(id) })
	})
	removeBtn.Importance = widget.LowImportance

	tools := container.NewHBox(upBtn, downBtn, colorBtn, renameBtn, removeBtn)
	// The active account needs no Switch button; its badge already says so.
	if !parallel && active {
		return tools
	}
	return container.NewBorder(nil, nil, tools, primary)
}

func usageMeter(m usage.Meter) fyne.CanvasObject {
	frac := m.Utilization / 100
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	name := widget.NewLabelWithStyle(m.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	value := fmt.Sprintf("%.0f%%", m.Utilization)
	if count := m.CountLabel(); count != "" {
		value = count
	}
	right := widget.NewLabel(value)
	right.Alignment = fyne.TextAlignTrailing

	var detail string
	if count := m.CountLabel(); count != "" {
		detail = fmt.Sprintf("%.0f%%", m.Utilization)
	}
	if !m.ResetsAt.IsZero() {
		reset := "Resets " + m.ResetsAt.Local().Format("Mon 3:04pm")
		if detail != "" {
			detail += " · " + reset
		} else {
			detail = reset
		}
	}
	sub := widget.NewLabel(detail)
	sub.Importance = widget.LowImportance
	if detail == "" {
		sub.Hide()
	}
	return vbox(4,
		container.NewBorder(nil, nil, name, right),
		newThinMeter(float32(frac)),
		sub,
	)
}

// roundedPanel is a padded surface with a hairline border. Colors are read
// again in Refresh so a light/dark switch repaints the card.
type roundedPanel struct {
	widget.BaseWidget
	inner fyne.CanvasObject
}

func newPanel(inner fyne.CanvasObject) *roundedPanel {
	p := &roundedPanel{inner: container.NewPadded(inner)}
	p.ExtendBaseWidget(p)
	return p
}

func (p *roundedPanel) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = 12
	bg.StrokeWidth = 1
	bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	return &panelRenderer{panel: p, bg: bg}
}

type panelRenderer struct {
	panel *roundedPanel
	bg    *canvas.Rectangle
}

func (r *panelRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.panel.inner.Resize(size)
	r.panel.inner.Move(fyne.NewPos(0, 0))
}

func (r *panelRenderer) MinSize() fyne.Size { return r.panel.inner.MinSize() }

func (r *panelRenderer) Refresh() {
	r.bg.FillColor = theme.Color(theme.ColorNameInputBackground)
	r.bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	r.bg.Refresh()
	r.panel.inner.Refresh()
}

func (r *panelRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.panel.inner}
}

func (r *panelRenderer) Destroy() {}

// thinMeter is a short rounded utilization bar, in the accent color unless
// fill is set.
type thinMeter struct {
	widget.BaseWidget
	frac float32
	fill color.Color
}

func newThinMeter(frac float32) *thinMeter {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	m := &thinMeter{frac: frac}
	m.ExtendBaseWidget(m)
	return m
}

func (m *thinMeter) fillColor() color.Color {
	if m.fill != nil {
		return m.fill
	}
	return uitheme.Accent
}

func (m *thinMeter) MinSize() fyne.Size { return fyne.NewSize(80, 6) }

func (m *thinMeter) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(uitheme.CurrentGridLine())
	track.CornerRadius = 3
	fill := canvas.NewRectangle(m.fillColor())
	fill.CornerRadius = 3
	return &thinMeterRenderer{meter: m, track: track, fill: fill}
}

type thinMeterRenderer struct {
	meter *thinMeter
	track *canvas.Rectangle
	fill  *canvas.Rectangle
}

func (r *thinMeterRenderer) Layout(size fyne.Size) {
	r.track.Resize(size)
	w := size.Width * r.meter.frac
	if r.meter.frac > 0 && w < 4 {
		w = 4
	}
	r.fill.Resize(fyne.NewSize(w, size.Height))
}

func (r *thinMeterRenderer) MinSize() fyne.Size { return r.meter.MinSize() }

func (r *thinMeterRenderer) Refresh() {
	r.track.FillColor = uitheme.CurrentGridLine()
	r.track.Refresh()
	r.fill.FillColor = r.meter.fillColor()
	r.fill.Refresh()
}

func (r *thinMeterRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill}
}

func (r *thinMeterRenderer) Destroy() {}
