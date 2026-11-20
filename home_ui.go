package main

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"github.com/lavluda/cd-switcher/internal/agents"
	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/sessionlog"
	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// homeColors is the Home panel's palette: macOS system colours, light and
// dark. It is read at build time, and the panel is rebuilt on every redraw,
// so a light/dark switch shows on the next open.
type homeColors struct {
	win, card, fill, fill2, sep, label, label2 color.Color
	blue, blueText, green, greenText, greenBg  color.Color
	redText, seg, onBlue                       color.Color
}

// homeDark is a variable so a test can render the dark palette; the test
// app has no real light/dark setting.
var homeDark = uitheme.Dark

func homePalette() homeColors {
	if homeDark() {
		return homeColors{
			win: rgb(0x1C1C1E), card: rgb(0x2C2C2E),
			fill: rgba(120, 120, 128, 0x52), fill2: rgba(120, 120, 128, 0x2E), sep: rgba(255, 255, 255, 0x1A),
			label: rgb(0xF5F5F7), label2: rgb(0xA1A1A6),
			blue: rgb(0x0A84FF), blueText: rgb(0x5AB0FF),
			green: rgb(0x30D158), greenText: rgb(0x30D158), greenBg: rgba(48, 209, 88, 0x29),
			redText: rgb(0xFF6961), seg: rgba(255, 255, 255, 0x2E), onBlue: color.White,
		}
	}
	return homeColors{
		win: rgb(0xF5F5F7), card: rgb(0xFFFFFF),
		fill: rgba(120, 120, 128, 0x24), fill2: rgba(120, 120, 128, 0x14), sep: rgba(60, 60, 67, 0x21),
		label: rgb(0x1D1D1F), label2: rgb(0x6E6E73),
		blue: rgb(0x007AFF), blueText: rgb(0x0066CC),
		green: rgb(0x34C759), greenText: rgb(0x1E7B34), greenBg: rgba(52, 199, 89, 0x24),
		redText: rgb(0xC4281C), seg: rgb(0xFFFFFF), onBlue: color.White,
	}
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}
func rgba(r, g, b, a uint8) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: a}
}

// Range filter for the usage card. The session logs only reach back
// sessionlog.Window (about a month), so longer ranges would be wrong.
var homeRanges = []string{"7 days", "30 days", "Month"}

// panelTab is the page shown in the panel, by its Settings page title; ""
// is Home. It resets to Home each time the panel opens.
var panelTab string

func (a *app) redrawPanel() {
	if a.panelWin != nil {
		a.panelWin.SetContent(a.panelContent())
	}
}

// panelContent builds the Home panel: the active account's limits first, then
// the other accounts, usage and recent sessions.
func (a *app) panelContent() fyne.CanvasObject {
	c := homePalette()

	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	active, hasActive := a.cfg.Active()
	pending := a.pendingLogin
	parallel := a.cfg.IsParallel()
	codexAccts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	activeCodex := a.cfg.ActiveCodex
	sessions := append([]agents.Session(nil), a.agentSessions...)
	autoSwitch := a.cfg.AutoSwitchOnLimit
	autoSwitchAt := a.cfg.AutoSwitchPercent()
	a.mu.Unlock()

	if panelTab != "" {
		return homeFrame(c, container.NewBorder(container.New(layout.NewCustomPaddedLayout(0, 8, 0, 0), a.homeToolbar(c, pending, sessions)), nil, nil, nil, a.panelPage(panelTab)))
	}

	a.statsMu.Lock()
	codexCache := map[string]codexEntry{}
	for k, v := range a.codexCache {
		codexCache[k] = v
	}
	var lastSync time.Time
	var email, plan string
	if hasActive && a.statsCache != nil {
		if e, ok := a.statsCache[active.ID]; ok {
			lastSync, email, plan = e.fetchedAt, e.email, e.plan
		}
	}
	a.statsMu.Unlock()

	running := map[string]bool{}
	if parallel {
		if dirs, err := claude.RunningDataDirs(); err == nil {
			for _, d := range dirs {
				running[d] = true
			}
		}
	}

	u := &a.usage
	if u.scanner == nil {
		u.scanner = sessionlog.NewScanner()
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	switch panelRangeIdx {
	case 0:
		from = midnight.AddDate(0, 0, -6)
	case 1:
		from = midnight.AddDate(0, 0, -29)
	}
	var rep sessionlog.Report
	if !u.scannedAt.IsZero() {
		rep = u.scanner.Report(from, "", false)
	}

	session, week, sessionAt, weekAt, fromHistory, known := a.limitDetails(active.ID)

	top := vbox(12,
		a.homeToolbar(c, pending, sessions),
		container.New(bentoCols{widths: []float32{0, 330}, gap: 12, minH: 190},
			a.homeActiveCard(c, active, hasActive, email, plan, session, week, sessionAt, weekAt, fromHistory, known),
			a.homeAccountsCard(c, profiles, active.ID, hasActive, parallel, running, codexAccts, activeCodex, codexCache, autoSwitch, autoSwitchAt),
		),
	)
	mid := container.New(bentoCols{widths: []float32{0, 330}, gap: 12},
		a.homeUsageCard(c, rep, from, now),
		a.homeSessionsCard(c, sessions),
	)

	return homeFrame(c, container.NewBorder(top, a.homeFooter(c, lastSync), nil, nil, container.New(layout.NewCustomPaddedLayout(12, 12, 0, 0), mid)))
}

// homeFrame is the panel's rounded window surface around a page.
func homeFrame(c homeColors, body fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(c.win)
	bg.CornerRadius = 16
	bg.StrokeColor = c.sep
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(12, 10, 14, 14), body))
}

// panelPage builds the Settings page with that title, to show under the
// toolbar instead of the Home cards.
func (a *app) panelPage(title string) fyne.CanvasObject {
	for _, pg := range settingsPages() {
		if pg.title == title {
			return pg.build(a)
		}
	}
	return layout.NewSpacer()
}

// ---------- building blocks ----------

func homeText(s string, size float32, bold bool, c color.Color) *canvas.Text {
	return hudTextObj(s, size, bold, c)
}

func homeCard(c homeColors, padV, padH float32, body fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(c.card)
	bg.CornerRadius = 16
	bg.StrokeColor = c.sep
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(padV, padV, padH, padH), body))
}

func homePill(text string, fg, bg color.Color) fyne.CanvasObject {
	rect := canvas.NewRectangle(bg)
	rect.CornerRadius = 9
	return container.NewCenter(container.NewStack(rect, container.New(layout.NewCustomPaddedLayout(2, 2, 8, 8), homeText(text, 11, true, fg))))
}

func homeRule(c homeColors) fyne.CanvasObject {
	r := canvas.NewRectangle(c.sep)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// homeBtn is a pill button: primary (blue), or a quiet filled one.
func homeBtn(c homeColors, label string, primary bool, tap func()) fyne.CanvasObject {
	if primary {
		return container.NewCenter(newPayButton(label, c.onBlue, c.blue, nil, c.blueText, 11.5, true, 5, 14, 14, tap))
	}
	return container.NewCenter(newPayButton(label, c.label, c.fill, nil, c.fill, 11.5, true, 5, 12, 14, tap))
}

// homeSegmented is a capsule of options with the selected one raised.
func homeSegmented(c homeColors, labels []string, selected int, size float32, padH float32, tap func(int)) fyne.CanvasObject {
	items := make([]fyne.CanvasObject, len(labels))
	for i, l := range labels {
		i := i
		if i == selected {
			items[i] = newPayButton(l, c.label, c.seg, c.sep, c.seg, size, true, 4, padH, 13, func() { tap(i) })
		} else {
			items[i] = newPayButton(l, c.label, color.Transparent, nil, c.fill2, size, false, 4, padH, 13, func() { tap(i) })
		}
	}
	bg := canvas.NewRectangle(c.fill)
	bg.CornerRadius = 15
	return container.NewCenter(container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), container.New(layout.NewCustomPaddedHBoxLayout(2), items...))))
}

func homeMeter(c homeColors, left float64) fyne.CanvasObject {
	m := newThinMeter(float32(clamp01(left / 100)))
	m.fill = c.blue
	m.h = 8
	return m
}

// homeCaption is the muted small label above a figure.
func homeCaption(c homeColors, s string) fyne.CanvasObject { return homeText(s, 11.5, true, c.label2) }

func homeLink(c homeColors, label string, tap func()) fyne.CanvasObject {
	return container.NewCenter(newPayButton(label, c.blueText, color.Transparent, nil, c.fill2, 11.5, false, 3, 6, 10, tap))
}

// resetPhrase reads "today 9:00 PM · in 3h 42m" for a future reset time.
func resetPhrase(t time.Time, now time.Time) string { return resetAt(t, now, true) }

// resetAt is resetPhrase with the countdown optional, for tight rows.
func resetAt(t time.Time, now time.Time, countdown bool) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	day := t.Format("Mon")
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		day = "today"
	}
	s := day + " " + t.Format("3:04 PM")
	if d := t.Sub(now); countdown && d > 0 {
		switch {
		case d < time.Hour:
			s += fmt.Sprintf(" · in %dm", int(d.Minutes()))
		case d < 48*time.Hour:
			s += fmt.Sprintf(" · in %dh %dm", int(d.Hours()), int(d.Minutes())%60)
		default:
			s += fmt.Sprintf(" · in %dd %dh", int(d.Hours())/24, int(d.Hours())%24)
		}
	}
	return s
}

// ---------- toolbar and footer ----------

func (a *app) homeToolbar(c homeColors, pending bool, sessions []agents.Session) fyne.CanvasObject {
	brand := container.New(layout.NewCustomPaddedHBoxLayout(8), payBrandBadge())
	if !pending {
		// Save account takes the name's room, or the tabs would not fit.
		brand.Add(container.NewCenter(homeText("CD Switcher", 14, true, c.label)))
	}

	tabs := []struct{ label, page string }{
		{"Home", ""}, {"Usage", "Usage"}, {"Accounts", "Accounts"}, {"Switching", "Switching"},
		{"Codex", "Codex & Copilot"}, {"Providers", "Providers"}, {"MCP", "MCP servers"},
		{"Skills", "Skills"}, {"Instructions", "Instructions"}, {"General", "General"}, {"Logs", "Logs"},
	}
	var nav []fyne.CanvasObject
	for _, t := range tabs {
		t := t
		if t.page == panelTab {
			nav = append(nav, newPayButton(t.label, c.label, c.seg, c.sep, c.seg, 12, true, 5, 10, 15, func() {}))
		} else {
			nav = append(nav, newPayButton(t.label, c.label, color.Transparent, nil, c.fill2, 12, false, 5, 8, 15, func() { panelTab = t.page; a.redrawPanel() }))
		}
	}
	navBg := canvas.NewRectangle(c.fill)
	navBg.CornerRadius = 18
	navStrip := container.NewStack(navBg, container.New(layout.NewCustomPaddedLayout(3, 3, 3, 3), container.New(layout.NewCustomPaddedHBoxLayout(2), nav...)))

	live := 0
	for _, s := range sessions {
		if s.State != agents.Done {
			live++
		}
	}
	inbox := "Inbox"
	if live > 0 {
		inbox = fmt.Sprintf("Inbox %d", live)
	}
	right := container.New(layout.NewCustomPaddedHBoxLayout(6))
	if pending {
		right.Add(homeBtn(c, "Save account", true, func() { a.runOp(a.doSaveAccount) }))
	}
	if a.statsRefreshing.Load() || a.usage.scanning.Load() {
		right.Add(container.NewCenter(newPayButton("↻ Refreshing…", c.label2, c.fill, nil, c.fill, 11.5, true, 5, 12, 14, nil)))
	} else {
		right.Add(homeBtn(c, "↻ Refresh", false, a.refreshAll))
	}
	// Every page is a tab now, so there is no Settings button; the inbox
	// switch lives on General.
	right.Add(homeBtn(c, inbox, false, func() { panelTab = "General"; a.redrawPanel() }))

	// The drag handle sits behind the toolbar, so its gaps and the brand move the window.
	return container.NewStack(&panelDrag{}, container.NewBorder(nil, nil, brand, right, container.NewCenter(navStrip)))
}

// refreshAll re-reads every account's limits and the usage logs. The toolbar
// shows "Refreshing…" until both finish, then redraws.
func (a *app) refreshAll() {
	if a.usage.scanner == nil {
		a.usage.scanner = sessionlog.NewScanner()
	}
	a.usage.scannedAt = time.Time{}
	a.scanUsage()
	a.statsRefreshing.Store(true) // set now so the redraw below shows the busy state
	a.redrawPanel()
	go func() {
		a.statsRefreshing.Store(false)
		a.refreshStats(0)
		for a.usage.scanning.Load() {
			time.Sleep(100 * time.Millisecond)
		}
		fyne.Do(a.redrawSettings)
	}()
}

func (a *app) homeFooter(c homeColors, lastSync time.Time) fyne.CanvasObject {
	launch := func(label, cmd string) fyne.CanvasObject {
		return homeBtn(c, label, false, func() { a.hidePanel(); a.openInTerminal(cmd) })
	}
	left := container.New(layout.NewCustomPaddedHBoxLayout(6),
		container.NewCenter(homeText("Open in Terminal", 11.5, false, c.label2)),
		launch("Claude Code", "claude"), launch("Codex", "codex"), launch("OpenCode", "opencode"),
	)

	sync := "Not synced yet"
	dot := c.label2
	if !lastSync.IsZero() {
		dot = c.green
		if m := int(time.Since(lastSync).Minutes()); m <= 0 {
			sync = "Synced just now"
		} else {
			sync = fmt.Sprintf("Synced %d min ago", m)
		}
	}
	right := container.New(layout.NewCustomPaddedHBoxLayout(8),
		container.NewCenter(container.NewGridWrap(fyne.NewSize(7, 7), canvas.NewCircle(dot))),
		container.NewCenter(homeText(sync, 11.5, false, c.label2)),
		homeLink(c, "Quit", func() { a.fyneApp.Quit() }),
	)
	return container.NewBorder(nil, nil, left, right)
}

// ---------- active account ----------

func (a *app) homeActiveCard(c homeColors, active profile.Profile, hasActive bool, email, plan string,
	session, week float64, sessionAt, weekAt time.Time, fromHistory, known bool) fyne.CanvasObject {

	title, sub := "No account", "Use Add account on the Accounts tab to capture one"
	if hasActive {
		title = clip(active.Label, 28)
		sub = "Claude"
		if email != "" && !strings.EqualFold(email, active.Label) {
			sub = email + " · Claude"
		}
		if plan != "" {
			sub += " · " + strings.ToUpper(plan[:1]) + plan[1:] + " plan"
		}
	}
	head := container.NewBorder(nil, nil,
		container.New(layout.NewCustomPaddedHBoxLayout(10),
			avatarCircle(profileInitials(title), color.White, c.blue, 38),
			vbox(1,
				container.New(layout.NewCustomPaddedHBoxLayout(8), homeText(title, 15, true, c.label), homePill("Active", c.greenText, c.greenBg)),
				homeText(clip(sub, 52), 12, false, c.label2),
			)),
		a.homePaceLine(c, active, hasActive, sessionAt, known),
	)

	var meters fyne.CanvasObject
	if known {
		now := time.Now()
		meters = container.New(bentoCols{widths: []float32{0, 1, 0}, gap: 22},
			a.homeLimit(c, "5-hour session", session, sessionAt, fromHistory, now),
			homeRule(c),
			a.homeLimit(c, "Weekly limit", week, weekAt, fromHistory, now),
		)
	} else {
		meters = homeText("Limits appear after the next refresh.", 12, false, c.label2)
	}
	return homeCard(c, 16, 20, vbox(18, head, meters))
}

// homePaceLine says when the session will fill, only when the burn rate in
// the usage history actually projects that.
func (a *app) homePaceLine(c homeColors, active profile.Profile, hasActive bool, resetsAt time.Time, known bool) fyne.CanvasObject {
	if !hasActive || !known {
		return layout.NewSpacer()
	}
	samples, err := usage.LoadHistory(a.historyPath(active.ID))
	if err != nil {
		return layout.NewSpacer()
	}
	eta, ok := usage.ForecastSession(samples, time.Now(), resetsAt)
	if !ok {
		return layout.NewSpacer()
	}
	return container.NewCenter(homeText("At this pace the session fills about "+eta.Local().Format("3:04 PM"), 12, true, c.redText))
}

func (a *app) homeLimit(c homeColors, title string, usedPct float64, resets time.Time, fromHistory bool, now time.Time) fyne.CanvasObject {
	used := math.Min(100, math.Max(0, usedPct))
	left := 100 - used
	big := c.label
	when := "Resets " + resetPhrase(resets, now)
	switch {
	case left <= 0:
		big = c.redText
		when = "Limit reached · resets " + resetPhrase(resets, now)
	case fromHistory:
		when = readingPhrase(resets, true)
	case resets.IsZero() || !resets.After(now):
		when = ""
	}
	figure := container.NewHBox(
		homeText(fmt.Sprintf("%.0f%%", left), 40, true, big),
		container.NewVBox(layout.NewSpacer(), container.New(layout.NewCustomPaddedLayout(0, 6, 0, 0), homeText("left", 14, false, c.label2))),
	)
	return container.New(layout.NewCustomPaddedVBoxLayout(8),
		homeCaption(c, title),
		figure,
		homeMeter(c, left),
		container.NewBorder(nil, nil, homeText(fmt.Sprintf("%.0f%% used", used), 11.5, false, c.label2), homeText(when, 11.5, false, c.label2)),
	)
}

// ---------- other accounts ----------

func (a *app) homeAccountsCard(c homeColors, profiles []profile.Profile, activeID string, hasActive, parallel bool, running map[string]bool,
	codexAccts []profile.CodexAccount, activeCodex string, codexCache map[string]codexEntry, autoSwitch bool, autoAt int) fyne.CanvasObject {

	head := container.NewBorder(nil, nil, homeText("Other accounts", 13, true, c.label), homeLink(c, "Add account", func() { a.hidePanel(); a.runOp(a.doAddAccount) }))

	type row struct {
		initial, title, sub string
		subRed              bool
		inUse               bool
		btn                 string
		tap                 func()
	}
	var rows []row
	now := time.Now()
	for _, p := range profiles {
		p := p
		on := hasActive && p.ID == activeID
		if parallel {
			dir, _ := filepath.Abs(a.store.ProfileDir(p.ID))
			on = running[filepath.Clean(dir)]
		} else if on {
			continue
		}
		r := row{initial: profileInitials(p.Label), title: p.Label, sub: "Limits appear after the next refresh", inUse: on, btn: "Switch",
			tap: func() { a.runOp(func() error { return a.doSwitch(p.ID) }) }}
		if parallel {
			r.btn = "Open"
		}
		if s, w, sAt, wAt, _, ok := a.limitDetails(p.ID); ok {
			switch {
			case w >= 100:
				r.sub, r.subRed = "Weekly limit reached · resets "+resetAt(wAt, now, false), true
			case s >= 100:
				r.sub, r.subRed = "Session limit reached · resets "+resetAt(sAt, now, false), true
			default:
				r.sub = fmt.Sprintf("%.0f%% session · %.0f%% weekly left", 100-s, 100-w)
			}
		}
		rows = append(rows, r)
	}
	for _, x := range codexAccts {
		x := x
		r := row{initial: "Cx", title: "Codex · " + x.Label, sub: "Codex account", inUse: x.ID == activeCodex, btn: "Switch",
			tap: func() { a.runOp(func() error { return a.doCodexSwitch(x.ID) }) }}
		if e, ok := codexCache[x.ID]; ok && !e.fetchedAt.IsZero() && e.quota.Session.Seconds > 0 {
			r.sub = fmt.Sprintf("%.0f%% left this session", 100-e.quota.Session.Percent)
		}
		rows = append(rows, r)
	}

	const maxRows = 3
	list := container.New(layout.NewCustomPaddedVBoxLayout(0))
	if len(rows) == 0 {
		list.Add(container.New(layout.NewCustomPaddedLayout(10, 10, 0, 0), homeText("Only one account so far. Add another to switch between them.", 12, false, c.label2)))
	}
	for i, r := range rows {
		if i == maxRows {
			list.Add(container.New(layout.NewCustomPaddedLayout(8, 4, 0, 0), homeLink(c, fmt.Sprintf("%d more in Accounts", len(rows)-maxRows), func() { a.showTab("Accounts") })))
			break
		}
		subCol := c.label2
		if r.subRed {
			subCol = c.redText
		}
		var right fyne.CanvasObject = homeBtn(c, r.btn, false, r.tap)
		if r.inUse {
			right = homePill("In use", c.greenText, c.greenBg)
		}
		line := container.NewBorder(nil, nil,
			avatarCircle(r.initial, c.label, c.fill, 30), right,
			container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), vbox(0, homeText(clip(r.title, 22), 12.5, true, c.label), homeText(clip(r.sub, 34), 11, false, subCol))),
		)
		if i > 0 {
			list.Add(homeRule(c))
		}
		list.Add(container.New(layout.NewCustomPaddedLayout(7, 7, 0, 0), line))
	}

	toggle := newToggleSwitch(autoSwitch, func(on bool) { a.runOp(func() error { return a.doSetAutoSwitch(on) }) })
	toggle.tint = c.green
	toggle.SetDisabled(len(profiles) < 2)
	auto := container.NewBorder(nil, nil, nil, container.NewCenter(toggle),
		vbox(0, homeText("Auto-switch", 12.5, true, c.label), homeText(fmt.Sprintf("At %d%% used, go to the freest account", autoAt), 11, false, c.label2)))

	return homeCard(c, 14, 16, container.NewBorder(vbox(4, head), vbox(8, homeRule(c), auto), nil, nil, list))
}

// ---------- usage ----------

func (a *app) homeUsageCard(c homeColors, rep sessionlog.Report, from, now time.Time) fyne.CanvasObject {
	head := container.NewBorder(nil, nil,
		container.New(layout.NewCustomPaddedHBoxLayout(8), homeText("Usage", 13, true, c.label),
			container.NewCenter(homeText(from.Format("Jan 2")+" – "+now.Format("Jan 2"), 11.5, false, c.label2))),
		homeSegmented(c, homeRanges, panelRangeIdx, 11.5, 10, func(i int) { panelRangeIdx = i; a.redrawPanel() }),
	)

	if rep.Total.Requests == 0 {
		return homeCard(c, 14, 18, container.NewBorder(head, nil, nil, nil,
			container.NewCenter(homeText("No usage logged in this range.", 12, false, c.label2))))
	}
	tot := rep.Total

	stat := func(l, v string) fyne.CanvasObject {
		return container.NewBorder(homeRule(c), nil, nil, nil,
			container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), container.NewBorder(nil, nil, homeText(l, 12, false, c.label2), homeText(v, 12, true, c.label))))
	}
	left := container.NewBorder(
		vbox(3, homeText(money(tot.Cost), 32, true, c.label), homeText("Estimated value at API prices", 11.5, false, c.label2)),
		vbox(0,
			stat("Tokens", tokens(tot.Tokens())),
			stat("Requests", count(int64(tot.Requests))),
			stat("Cache hit rate", fmt.Sprintf("%.1f%%", tot.CacheHitRate()*100)),
		), nil, nil)

	byTool := map[string]float64{}
	for _, r := range rep.ByModel {
		byTool[r.Tool] += r.Cost
	}
	tools := []struct{ name, key string }{
		{"Claude Code", sessionlog.Claude}, {"Codex CLI", sessionlog.Codex}, {"Cursor", sessionlog.Cursor}, {"OpenCode", sessionlog.OpenCode},
	}
	right := container.New(layout.NewCustomPaddedVBoxLayout(10), homeCaption(c, "By tool"))
	for _, t := range tools {
		share := 0.0
		if tot.Cost > 0 {
			share = byTool[t.key] / tot.Cost * 100
		}
		cost, costCol := money(byTool[t.key]), c.label
		if byTool[t.key] == 0 {
			costCol = c.label2
		}
		pct := homeText(fmt.Sprintf("%.0f%%", share), 12, false, c.label2)
		pct.Alignment = fyne.TextAlignTrailing
		amt := homeText(cost, 12, true, costCol)
		amt.Alignment = fyne.TextAlignTrailing
		right.Add(container.NewBorder(nil, nil,
			container.NewGridWrap(fyne.NewSize(92, 18), homeText(t.name, 12, false, c.label)),
			container.NewHBox(container.NewGridWrap(fyne.NewSize(38, 18), pct), container.NewGridWrap(fyne.NewSize(66, 18), amt)),
			container.NewCenter(homeMeter(c, share)),
		))
	}

	cols := container.New(bentoCols{widths: []float32{210, 1, 0}, gap: 20}, left, homeRule(c), right)
	return homeCard(c, 14, 18, container.NewBorder(head, nil, nil, nil, container.New(layout.NewCustomPaddedLayout(12, 0, 0, 0), cols)))
}

// ---------- sessions ----------

func (a *app) homeSessionsCard(c homeColors, sessions []agents.Session) fyne.CanvasObject {
	head := container.NewBorder(nil, nil, homeText("Recent sessions", 13, true, c.label), homeLink(c, "View all", func() { a.showTab("Usage") }))

	waiting := 0
	for _, s := range sessions {
		if s.State == agents.Waiting {
			waiting++
		}
	}
	note, noteFg := "No sessions are waiting for you", c.label2
	if waiting > 0 {
		note, noteFg = "1 session is waiting for you", c.redText
		if waiting > 1 {
			note = fmt.Sprintf("%d sessions are waiting for you", waiting)
		}
	}
	banner := paySubtleBox(7, 10, 10, c.fill2, homeText(note, 11.5, waiting > 0, noteFg))

	live, done := splitSessions(sessions)
	list := container.New(layout.NewCustomPaddedVBoxLayout(0))
	const maxRows = 4
	for i, s := range append(live, done...) {
		if i == maxRows {
			break
		}
		s := s
		state, stateCol := "Working", c.greenText
		switch s.State {
		case agents.Waiting:
			state, stateCol = "Needs you", c.redText
		case agents.Done:
			state, stateCol = "Finished", c.label2
		}
		line := container.NewBorder(nil, nil, nil, homeText(s.At.Local().Format("3:04 PM"), 11.5, false, c.label2),
			vbox(0, homeText(clip(s.Project(), 26), 12.5, true, c.label),
				container.New(layout.NewCustomPaddedHBoxLayout(4), homeText(harness.Title(s.Tool)+" ·", 11, false, c.label2), homeText(state, 11, s.State != agents.Done, stateCol))))
		if i > 0 {
			list.Add(homeRule(c))
		}
		list.Add(newPayInteractiveRow(line, color.Transparent, c.fill2, 8, 7, 4, func() { a.hidePanel(); a.openSession(s) }))
	}
	if len(sessions) == 0 {
		list.Add(container.New(layout.NewCustomPaddedLayout(10, 0, 0, 0), homeText("Sessions from Claude Code, Codex, Cursor and OpenCode appear here.", 11.5, false, c.label2)))
	}
	return homeCard(c, 14, 16, container.NewBorder(vbox(8, head, banner), nil, nil, nil, container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), list)))
}
