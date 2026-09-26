package main

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/login"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// settingsContent builds the settings window: a mode switch, then one panel
// per account with its live plan meters. Reads state under the lock; runs on
// the UI thread.
func (a *app) settingsContent() fyne.CanvasObject {
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	activeID := a.cfg.ActiveProfile
	parallel := a.cfg.IsParallel()
	launchAtLogin := a.cfg.LaunchAtLogin
	warnAt := a.cfg.WarnPercent()
	a.mu.Unlock()
	_, best, hasBest := a.freest(profiles)

	title := widget.NewLabelWithStyle("Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	sub := widget.NewLabel("Accounts, plan limits, and spend")
	sub.Importance = widget.LowImportance
	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { go a.refreshStats() })
	refreshBtn.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, container.NewVBox(title, sub), refreshBtn)

	topItems := []fyne.CanvasObject{header, a.modeControl(parallel), a.alertControl(warnAt)}
	if row := a.loginRow(launchAtLogin); row != nil {
		topItems = append(topItems, row)
	}
	topItems = append(topItems, sectionLabel("Accounts"))
	top := container.NewVBox(topItems...)

	cards := container.NewVBox()
	if len(profiles) == 0 {
		cards.Add(newPanel(widget.NewLabel("No accounts yet. Use Add account… from the menu.")))
	}
	for i, p := range profiles {
		if i > 0 {
			cards.Add(vGap(8))
		}
		cards.Add(a.accountCard(p, p.ID == activeID, parallel, hasBest && p.ID == best.ID, i == 0, i == len(profiles)-1))
	}

	note := widget.NewLabel("Session and weekly figures are how full each limit is. Extra usage is spend past the plan. Token counts appear when Claude's usage response includes them.")
	note.Wrapping = fyne.TextWrapWord
	note.Importance = widget.LowImportance

	return container.NewPadded(container.NewBorder(
		top,
		note, nil, nil,
		container.NewVScroll(cards),
	))
}

func (a *app) modeControl(parallel bool) fyne.CanvasObject {
	one := widget.NewButton("Switch accounts", func() {
		if parallel {
			a.runOp(func() error { return a.doSetMode(profile.ModeSwitch) })
		}
	})
	side := widget.NewButton("Side by side", func() {
		if !parallel {
			a.runOp(func() error { return a.doSetMode(profile.ModeParallel) })
		}
	})
	if parallel {
		side.Importance = widget.HighImportance
		one.Importance = widget.LowImportance
	} else {
		one.Importance = widget.HighImportance
		side.Importance = widget.LowImportance
	}
	hint := widget.NewLabel("Control-Option-S opens the next account.")
	hint.Importance = widget.LowImportance
	return newPanel(container.NewVBox(container.NewGridWithColumns(2, one, side), hint))
}

func (a *app) alertControl(current int) fyne.CanvasObject {
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
	label := widget.NewLabel("Limit alert")
	return newPanel(container.NewVBox(container.NewBorder(nil, nil, label, sel), hint))
}

func (a *app) loginRow(on bool) fyne.CanvasObject {
	if !login.Supported() {
		return nil
	}
	var suppress bool
	chk := widget.NewCheck("Open CD-Switcher at login", func(checked bool) {
		if suppress {
			return
		}
		a.runOp(func() error { return a.doSetLogin(checked) })
	})
	suppress = true
	chk.SetChecked(on)
	suppress = false
	return newPanel(chk)
}

func sectionLabel(text string) fyne.CanvasObject {
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return l
}

// accountCard is one account: name and plan, limit meters, spend, the recent
// session chart, and the account actions.
func (a *app) accountCard(p profile.Profile, active, parallel, mostRoom, first, last bool) fyne.CanvasObject {
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

	name := widget.NewLabelWithStyle(p.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	meta := container.NewHBox()
	if parallel && windowOpen {
		meta.Add(accentLabel("Open"))
	} else if !parallel && active {
		meta.Add(accentLabel("Active"))
	}
	if mostRoom {
		meta.Add(accentLabel("Most room"))
	}

	a.statsMu.Lock()
	e, ok := a.statsCache[id]
	a.statsMu.Unlock()

	var body []fyne.CanvasObject
	body = append(body, name)
	if len(meta.Objects) > 0 || (ok && e.plan != "") {
		if ok && e.plan != "" {
			plan := e.plan
			if e.tier != "" {
				plan += " · " + e.tier
			}
			planLabel := widget.NewLabel(plan)
			planLabel.Importance = widget.LowImportance
			meta.Add(planLabel)
		}
		if len(meta.Objects) > 0 {
			body = append(body, meta)
		}
	}

	switch {
	case ok && !e.fetchedAt.IsZero():
		status := widget.NewLabel("Updated " + e.fetchedAt.Local().Format("3:04pm"))
		if e.err != nil {
			status.SetText("Last reading " + e.fetchedAt.Local().Format("3:04pm"))
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
		oldest := widget.NewLabel(history[0].Time.Local().Format("3:04pm"))
		oldest.Importance = widget.LowImportance
		newest := widget.NewLabel("now")
		newest.Importance = widget.LowImportance
		body = append(body,
			chartLabel,
			newSparkline(history),
			container.NewBorder(nil, nil, oldest, newest),
		)
	}

	body = append(body, a.accountActions(id, active, parallel, windowOpen, first, last))
	return newPanel(container.NewVBox(body...))
}

func (a *app) accountActions(id string, active, parallel, windowOpen, first, last bool) fyne.CanvasObject {
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
		if active {
			primary.Disable()
		} else {
			primary.Importance = widget.HighImportance
		}
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
	renameBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		a.runOp(func() error { return a.doRename(id) })
	})
	renameBtn.Importance = widget.LowImportance
	removeBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		a.runOp(func() error { return a.doRemove(id) })
	})
	removeBtn.Importance = widget.LowImportance

	return container.NewBorder(nil, nil, container.NewHBox(upBtn, downBtn, renameBtn, removeBtn), primary)
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
	return container.NewVBox(
		container.NewBorder(nil, nil, name, right),
		newThinMeter(float32(frac)),
		sub,
	)
}

func accentLabel(text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Importance = widget.HighImportance
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

func vGap(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	r.SetMinSize(fyne.NewSize(0, h))
	return r
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

// thinMeter is a short rounded utilization bar.
type thinMeter struct {
	widget.BaseWidget
	frac float32
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

func (m *thinMeter) MinSize() fyne.Size { return fyne.NewSize(80, 6) }

func (m *thinMeter) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(uitheme.CurrentGridLine())
	track.CornerRadius = 3
	fill := canvas.NewRectangle(uitheme.Accent)
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
	r.fill.FillColor = uitheme.Accent
	r.fill.Refresh()
}

func (r *thinMeterRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill}
}

func (r *thinMeterRenderer) Destroy() {}
