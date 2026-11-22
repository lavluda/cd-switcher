package main

// The Accounts and Codex pages: one card per account with its limits.

import (
	"errors"
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
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/copilot"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/icon"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// ---------- Accounts ----------

func (a *app) accountsTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	activeID := a.cfg.ActiveProfile
	parallel := a.cfg.IsParallel()
	a.mu.Unlock()
	_, best, hasBest := a.freest(profiles)

	add := homeBtn(c, "Add account", true, func() {
		if panelTab != "" {
			a.hidePanel()
		}
		a.runOp(a.doAddAccount)
	})
	refresh := homeBtn(c, "Refresh", false, func() { go a.refreshStats(0) })
	items := []fyne.CanvasObject{pageHeader(c, "Accounts", "Your Claude accounts, their limits and recent use", refresh, add)}
	if len(profiles) == 0 {
		items = append(items, homeCard(c, 18, 22, homeText("No accounts yet. Add account captures the one Claude is signed in to.", 12, false, c.label2)))
	}
	for i, p := range profiles {
		items = append(items, a.accountCard(c, p, p.ColorIndex(i), p.ID == activeID, parallel, hasBest && p.ID == best.ID, i == 0, i == len(profiles)-1))
	}
	items = append(items, flushNote("Each meter shows how much of the limit is left. Token counts and extra-usage spend appear on a card when Claude reports them."))
	return pageScroll(items...)
}

// accountCard is one account: who it is and what it can do, its two limits,
// and the last day of session use.
func (a *app) accountCard(c homeColors, p profile.Profile, colorIdx int, active, parallel, mostRoom, first, last bool) fyne.CanvasObject {
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

	a.statsMu.Lock()
	e, ok := a.statsCache[id]
	a.statsMu.Unlock()

	// Who: name, status pills, plan.
	pills := container.New(layout.NewCustomPaddedHBoxLayout(6), homeText(clip(p.Label, 22), 15, true, c.label))
	if parallel && windowOpen || !parallel && active {
		label := "Active"
		if parallel {
			label = "Open"
		}
		pills.Add(homePill(label, c.greenText, c.greenBg))
	}
	if mostRoom {
		pills.Add(homePill("Most room", c.label2, c.fill))
	}
	sub := ""
	if ok && e.plan != "" {
		sub = strings.ToUpper(e.plan[:1]) + e.plan[1:]
		if e.tier != "" {
			sub += " · " + e.tier
		}
		if e.email != "" && !strings.EqualFold(e.email, p.Label) {
			sub = e.email + " · " + sub
		}
	}
	who := container.New(layout.NewCustomPaddedHBoxLayout(12),
		avatarCircle(profileInitials(p.Label), color.White, avatarTone(c, icon.SwatchColor(colorIdx)), 38),
		vbox(2, pills, homeText(clip(sub, 34), 12, false, c.label2)))

	// What it can do.
	var primary fyne.CanvasObject
	switch {
	case parallel && windowOpen:
		primary = homeBtn(c, "Close", false, func() { a.runOp(func() error { return a.doCloseParallel(id) }) })
	case parallel:
		primary = homeBtn(c, "Open", true, func() { a.runOp(func() error { return a.doOpenParallel(id) }) })
	case active:
		primary = container.NewCenter(newPayButton("In use", c.label2, c.fill2, nil, c.fill2, 11.5, true, 5, 14, 14, nil))
	default:
		primary = homeBtn(c, "Switch", true, func() { a.runOp(func() error { return a.doSwitch(id) }) })
	}
	more := []*fyne.MenuItem{
		fyne.NewMenuItem("Rename…", func() { a.runOp(func() error { return a.doRename(id) }) }),
		fyne.NewMenuItem("Change colour", func() { a.runOp(func() error { return a.doCycleColor(id) }) }),
	}
	if !first {
		more = append(more, fyne.NewMenuItem("Move up", func() { a.runOp(func() error { return a.doMove(id, -1) }) }))
	}
	if !last {
		more = append(more, fyne.NewMenuItem("Move down", func() { a.runOp(func() error { return a.doMove(id, 1) }) }))
	}
	more = append(more, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Remove…", func() { a.runOp(func() error { return a.doRemove(id) }) }))

	skip := container.New(layout.NewCustomPaddedHBoxLayout(8),
		container.NewCenter(newHomeCheck(c, p.NoAutoSwitch, true, func(on bool) { a.runOp(func() error { return a.doSetNoAutoSwitch(id, on) }) })),
		homeText("Skip in auto-switch", 12, false, c.label2))
	actions := container.New(layout.NewCustomPaddedHBoxLayout(8), primary, homeMore(c, more...))
	left := container.NewBorder(who, vbox(10, actions, skip), nil, nil)

	return homeCard(c, 18, 22, container.New(accountCols{},
		left, homeRule(c), a.accountLimits(c, id, e, ok), homeRule(c), a.accountHistory(c, id, e, ok)))
}

// accountLimits is the two headline limits, then any extra figures the
// response carried (model limits, tokens, spend).
func (a *app) accountLimits(c homeColors, id string, e statEntry, ok bool) fyne.CanvasObject {
	session, week, sessionAt, weekAt, fromHistory, known := a.limitDetails(id)
	if !known {
		return vmid(homeText("Usage will appear after the next refresh.", 12, false, c.label2))
	}
	now := time.Now()
	cols := container.New(bentoCols{gap: 22},
		pageLimit(c, "5-hour session", session, sessionAt, fromHistory, now),
		pageLimit(c, "Weekly limit", week, weekAt, fromHistory, now))
	var extra []string
	if ok && !fromHistory {
		for _, m := range e.stats.Meters()[min(2, len(e.stats.Meters())):] {
			line := fmt.Sprintf("%s %.0f%% used", m.Label, math.Min(100, math.Max(0, m.Utilization)))
			if n := m.CountLabel(); n != "" {
				line = m.Label + " " + n + " · " + fmt.Sprintf("%.0f%%", m.Utilization)
			}
			if !m.ResetsAt.IsZero() {
				line += " · resets " + resetAt(m.ResetsAt, now, false)
			}
			extra = append(extra, line)
		}
		if d := e.stats.Tokens.Detail(); d != "" {
			extra = append(extra, "Tokens  "+d)
		}
		if s := e.stats.ExtraUsage.SpendLabel(); s != "" {
			extra = append(extra, s)
		}
	}
	if len(extra) == 0 {
		return vmid(cols)
	}
	lines := make([]fyne.CanvasObject, len(extra))
	for i, l := range extra {
		lines[i] = homeText(l, 11.5, false, c.label2)
	}
	return vmid(vbox(12, cols, vbox(2, lines...)))
}

// accountHistory is the last day of session use as a line, with the
// seven-day summary under it.
func (a *app) accountHistory(c homeColors, id string, e statEntry, ok bool) fyne.CanvasObject {
	status := ""
	if ok && !e.fetchedAt.IsZero() {
		status = "Updated " + e.fetchedAt.Local().Format("3:04 PM")
		if e.err != nil {
			status = "Last reading " + e.fetchedAt.Local().Format("3:04 PM")
		}
		if e.fromClaudeCode {
			status += " · Claude Code"
		}
	}
	head := homeText("Session use, last day", 11.5, true, c.label2)

	history, err := usage.LoadHistory(a.historyPath(id))
	if err != nil {
		logf("stats: load history failed for %q: %v", id, err)
	}
	body := []fyne.CanvasObject{head}
	if len(history) >= 2 {
		body = append(body, newUsageLine(history, c.blue))
	} else {
		body = append(body, homeText("History appears after a few readings.", 12, false, c.label2))
	}
	if status != "" {
		body = append(body, homeText(status, 11, false, c.label2))
	}
	if d, err := usage.LoadDaily(a.store.DailyPath(id)); err == nil {
		if sum, days := d.Summary(time.Now(), 7); days > 0 {
			body = append(body, flushNote(fmt.Sprintf("Last 7 days: peak %.0f%% session, %.0f%% weekly. Limit hit %d times.", sum.PeakSession, sum.PeakWeek, sum.Hits)))
		}
	}
	return vmid(vbox(8, body...))
}

// usageLine draws the last day of session use, one point per hour (the peak
// reached in that hour), joined by a line. Hours with no reading are skipped.
type usageLine struct {
	widget.BaseWidget
	peaks []float64 // peak session % per hour, oldest first; -1 when none
	col   color.Color
}

const lineHours = 24

func newUsageLine(samples []usage.Sample, col color.Color) *usageLine {
	l := &usageLine{col: col, peaks: make([]float64, lineHours)}
	for i := range l.peaks {
		l.peaks[i] = -1
	}
	if len(samples) > 0 {
		end := samples[len(samples)-1].Time.Local().Truncate(time.Hour)
		for _, sm := range samples {
			i := lineHours - 1 - int(end.Sub(sm.Time.Local().Truncate(time.Hour))/time.Hour)
			if i >= 0 && i < lineHours {
				l.peaks[i] = max(l.peaks[i], math.Min(math.Max(sm.FiveHour, 0), 100))
			}
		}
	}
	l.ExtendBaseWidget(l)
	return l
}

func (l *usageLine) MinSize() fyne.Size { return fyne.NewSize(120, 52) }

func (l *usageLine) CreateRenderer() fyne.WidgetRenderer {
	r := &usageLineRenderer{l: l}
	for i := 1; i < lineHours; i++ {
		ln := canvas.NewLine(l.col)
		ln.StrokeWidth = 2
		r.lines = append(r.lines, ln)
	}
	return r
}

type usageLineRenderer struct {
	l     *usageLine
	lines []*canvas.Line
}

func (r *usageLineRenderer) Layout(size fyne.Size) {
	const pad = 2
	x := func(i int) float32 { return pad + (size.Width-2*pad)*float32(i)/float32(lineHours-1) }
	y := func(v float64) float32 { return pad + (size.Height-2*pad)*float32(1-v/100) }
	prev := -1
	for i := range r.lines {
		r.lines[i].Hide()
	}
	for i, v := range r.l.peaks {
		if v < 0 {
			continue
		}
		if prev >= 0 {
			// Draw the span from the previous reading; each span uses the line
			// slot of the hour it ends in.
			ln := r.lines[i-1]
			ln.Position1, ln.Position2 = fyne.NewPos(x(prev), y(r.l.peaks[prev])), fyne.NewPos(x(i), y(v))
			ln.Show()
		}
		prev = i
	}
}

func (r *usageLineRenderer) MinSize() fyne.Size { return r.l.MinSize() }
func (r *usageLineRenderer) Refresh() {
	for _, ln := range r.lines {
		ln.StrokeColor = r.l.col
		ln.Refresh()
	}
}
func (r *usageLineRenderer) Objects() []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, len(r.lines))
	for i, ln := range r.lines {
		out[i] = ln
	}
	return out
}
func (r *usageLineRenderer) Destroy() {}

// ---------- Codex and Copilot ----------

func (a *app) codexTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	accts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	active := a.cfg.ActiveCodex
	a.mu.Unlock()

	header := pageHeader(c, "Codex and Copilot", "Each Codex account keeps its own ChatGPT login. Running Codex sessions need a restart after a switch.",
		homeBtn(c, "Save current login", false, func() { a.runOp(a.doCodexAddCurrent) }),
		homeBtn(c, "Sign in…", true, a.startCodexLogin))
	items := []fyne.CanvasObject{header}

	switch {
	case !harness.Installed(harness.Codex, ""):
		items = append(items, homeCard(c, 18, 22, homeText("Codex is not installed on this computer (no "+harness.CodexHome()+" folder).", 12, false, c.label2)))
	case len(accts) == 0:
		items = append(items, homeCard(c, 18, 22, homeText("No Codex accounts yet. Save current login keeps the account Codex uses now; Sign in adds another one through your browser.", 12, false, c.label2)))
	}
	for i := 0; i < len(accts); i += 2 {
		pair := []fyne.CanvasObject{a.codexCard(c, accts[i], accts[i].ID == active)}
		if i+1 < len(accts) {
			pair = append(pair, a.codexCard(c, accts[i+1], accts[i+1].ID == active))
		}
		items = append(items, container.New(bentoCols{gap: 14}, pair...))
	}

	a.statsMu.Lock()
	cp := a.copilot
	a.statsMu.Unlock()
	if !cp.fetchedAt.IsZero() {
		items = append(items, copilotCard(c, cp.quota))
	} else if cp.err != nil && !errors.Is(cp.err, copilot.ErrNoLogin) {
		items = append(items, homeCard(c, 18, 22, homeText("GitHub Copilot: "+cp.err.Error(), 12, false, c.redText)))
	}
	return pageScroll(items...)
}

func (a *app) codexCard(c homeColors, x profile.CodexAccount, active bool) fyne.CanvasObject {
	id := x.ID
	name := container.New(layout.NewCustomPaddedHBoxLayout(6), homeText(clip(x.Label, 24), 15, true, c.label))
	if active {
		name.Add(homePill("Active", c.greenText, c.greenBg))
	}
	var info []string
	if !strings.EqualFold(x.Email, x.Label) && x.Email != "" {
		info = append(info, x.Email)
	}
	if x.Plan != "" {
		info = append(info, strings.ToUpper(x.Plan[:1])+x.Plan[1:])
	}
	who := container.New(layout.NewCustomPaddedHBoxLayout(12),
		avatarCircle(profileInitials(x.Label), color.White, c.blue, 38),
		vbox(2, name, homeText(clip(strings.Join(info, " · "), 34), 12, false, c.label2)))
	var head fyne.CanvasObject = who
	if !active {
		head = container.NewBorder(nil, nil, nil, homeBtn(c, "Use", true, func() { a.runOp(func() error { return a.doCodexSwitch(id) }) }), who)
	}

	a.statsMu.Lock()
	e, ok := a.codexCache[id]
	a.statsMu.Unlock()
	var body fyne.CanvasObject
	status := ""
	now := time.Now()
	switch {
	case ok && !e.fetchedAt.IsZero():
		var cells []fyne.CanvasObject
		if e.quota.Session.Seconds > 0 {
			cells = append(cells, pageLimit(c, "Session", e.quota.Session.Percent, e.quota.Session.ResetsAt, false, now))
		}
		if e.quota.Weekly.Seconds > 0 {
			cells = append(cells, pageLimit(c, "This week", e.quota.Weekly.Percent, e.quota.Weekly.ResetsAt, false, now))
		}
		body = container.New(bentoCols{gap: 22}, cells...)
		status = "Updated " + e.fetchedAt.Local().Format("3:04 PM")
		if e.err != nil {
			status = "Last reading " + e.fetchedAt.Local().Format("3:04 PM") + " · " + e.err.Error()
		}
	case ok && e.err != nil:
		body = homeText(clip(e.err.Error(), 70), 12, false, c.redText)
	default:
		body = homeText("Usage will appear after the next refresh.", 12, false, c.label2)
	}
	foot := container.NewBorder(nil, nil, nil,
		container.New(layout.NewCustomPaddedHBoxLayout(14),
			homeLink(c, "Rename", func() { a.runOp(func() error { return a.doCodexRename(id) }) }),
			homeRedLink(c, "Remove", func() { a.runOp(func() error { return a.doCodexRemove(id) }) })),
		homeText(clip(status, 40), 11.5, false, c.label2))
	return homeCard(c, 18, 22, vbox(18, head, body, vbox(10, homeRule(c), foot)))
}

func copilotCard(c homeColors, q copilot.Quota) fyne.CanvasObject {
	who := vbox(2, homeText("GitHub Copilot", 15, true, c.label), homeText(strings.TrimSpace(q.Login+" · "+q.Plan), 12, false, c.label2))
	var right fyne.CanvasObject = homeText("Unlimited premium requests", 12, false, c.label2)
	if !q.Unlimited {
		resets := ""
		if !q.ResetsAt.IsZero() {
			resets = "Resets " + q.ResetsAt.Local().Format("Jan 2")
		}
		right = vbox(9,
			container.NewBorder(nil, nil, homeCaption(c, "Premium requests"), homeText(resets, 12, false, c.label2)),
			homeMeter(c, 100-q.Percent),
			container.New(layout.NewCustomPaddedHBoxLayout(0), homeText(fmt.Sprintf("%d of %d left", q.Remaining, q.Total), 13, true, c.label),
				homeText(fmt.Sprintf(" · %.0f%% used", q.Percent), 13, false, c.label2)),
		)
	}
	return homeCard(c, 18, 22, container.New(bentoCols{widths: []float32{260, 0}, gap: 28}, vmid(who), vmid(right)))
}

// pageLimit is a limit's caption, how much is left as a big figure, its
// meter, and two short lines under it: how much is used and when it resets.
func pageLimit(c homeColors, title string, usedPct float64, resets time.Time, fromHistory bool, now time.Time) fyne.CanvasObject {
	used := math.Min(100, math.Max(0, usedPct))
	left := 100 - used
	big, usedCol := c.label, c.label2
	when := "Resets " + resetAt(resets, now, false)
	switch {
	case left <= 0:
		big, usedCol = c.redText, c.redText
		if fromHistory {
			when = readingPhrase(resets, true)
		}
	case fromHistory:
		when = readingPhrase(resets, true)
	case resets.IsZero() || !resets.After(now):
		when = ""
	}
	figure := container.NewHBox(
		homeText(fmt.Sprintf("%.0f%%", left), 30, true, big),
		container.NewVBox(layout.NewSpacer(), container.New(layout.NewCustomPaddedLayout(0, 4, 0, 0), homeText("left", 13, false, c.label2))),
	)
	usedText := fmt.Sprintf("%.0f%% used", used)
	if left <= 0 {
		usedText = "Limit reached"
	}
	lines := []fyne.CanvasObject{homeText(usedText, 11.5, left <= 0, usedCol)}
	if when != "" {
		lines = append(lines, homeText(when, 11.5, false, c.label2))
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(8), homeCaption(c, title), figure, homeMeter(c, left), vbox(2, lines...))
}

// avatarTone lifts a swatch that would vanish against the dark card.
func avatarTone(c homeColors, sw color.Color) color.Color {
	r, g, b, _ := sw.RGBA()
	if homeDark() && (r+g+b)/3 < 0x4000 {
		return rgb(0x636366)
	}
	return sw
}

// accountCols lays out an account card's five blocks (who, rule, limits,
// rule, history). In a narrow window the history block and its rule are
// dropped, so the card never needs a sideways scroll.
type accountCols struct{}

const accountWideAt = 800

func (accountCols) wide(w float32) bool { return w >= accountWideAt }

func (l accountCols) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if l.wide(size.Width) {
		bentoCols{widths: []float32{250, 1, 0, 1, 205}, gap: 20, minH: 150}.Layout(objs, size)
		objs[3].Show()
		objs[4].Show()
		return
	}
	objs[3].Hide()
	objs[4].Hide()
	bentoCols{widths: []float32{250, 1, 0}, gap: 20, minH: 150}.Layout(objs[:3], size)
}

func (accountCols) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return bentoCols{widths: []float32{250, 1, 0}, gap: 20, minH: 150}.MinSize(objs[:3])
}
