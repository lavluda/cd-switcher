package main

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"

	"github.com/lavluda/cd-switcher/internal/agents"
	"github.com/lavluda/cd-switcher/internal/sessionlog"
	"github.com/lavluda/cd-switcher/internal/usage"
)

const (
	panelTitle = "CD Switcher"
	panelW     = 960
	panelH     = 650
)

// panelRangeIdx is the Home usage range: 0: 7 days, 1: 30 days, 2: month.
var panelRangeIdx = 2

// ensurePanel installs the menu-bar panel the first time the tray is built.
// A click on the icon shows it; another click, or activating another app, hides it.
func (a *app) ensurePanel() {
	if a.panelWin != nil {
		return
	}
	drv, ok := a.fyneApp.Driver().(desktop.Driver)
	if !ok {
		return
	}
	w := drv.CreateSplashWindow()
	w.SetTitle(panelTitle)
	w.Resize(fyne.NewSize(panelW, panelH))
	w.SetFixedSize(true)
	w.SetContent(a.panelContent())
	w.SetCloseIntercept(a.hidePanel) // not w.Hide: off macOS panelOpen is the only record that it closed
	if a.noTray {
		// No icon to reopen it from: closing the only window ends the app.
		w.SetCloseIntercept(a.fyneApp.Quit)
	}
	w.Hide()
	a.panelWin = w
	a.desk.SetSystemTrayWindow(w)
	systray.SetOnTapped(a.onTrayTap)
	// Right-click keeps a short fallback. The left click is the panel.
	a.desk.SetSystemTrayMenu(fyne.NewMenu(panelTitle, fyne.NewMenuItem("Open", a.onTrayTap)))
}

func (a *app) onTrayTap() {
	fyne.Do(func() {
		if a.panelWin == nil {
			return
		}
		// On macOS, a click while the app is inactive reaches systray twice
		// (mouse-down monitor, then the button's mouse-up action); the second would hide the panel.
		if time.Since(a.trayTapAt) < 600*time.Millisecond {
			return
		}
		a.trayTapAt = time.Now()
		if a.panelShowing() {
			a.hidePanel() // resets panelOpen; a bare Hide left it true, so later taps never reopened (Linux)
			return
		}
		if a.usage.scanner == nil {
			a.usage.scanner = sessionlog.NewScanner()
		}
		if time.Since(a.usage.scannedAt) > 2*time.Minute {
			a.scanUsage()
		}
		h := menuPanelHeight()
		a.panelWin.SetContent(a.panelContent())
		a.panelWin.Resize(fyne.NewSize(panelW, h))
		a.panelWin.Show()
		a.panelOpen = true
		placePanel(panelTitle, panelW, h)
	})
}

func (a *app) hidePanel() {
	if a.panelWin != nil {
		a.panelWin.Hide()
	}
	a.panelOpen = false
}

// showTab switches the panel to the page with that title.
func (a *app) showTab(title string) {
	panelTab = title
	a.redrawPanel()
}

func profileInitials(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "CD"
	}
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == '@'
	})
	if len(words) >= 2 {
		r1 := []rune(words[0])
		r2 := []rune(words[1])
		if len(r1) > 0 && len(r2) > 0 {
			return strings.ToUpper(string(r1[0]) + string(r2[0]))
		}
	}
	runes := []rune(s)
	if len(runes) >= 2 {
		return strings.ToUpper(string(runes[:2]))
	}
	return strings.ToUpper(string(runes[0]))
}

func (a *app) limitDetails(id string) (session, week float64, sessionAt, weekAt time.Time, fromHistory, ok bool) {
	if id == "" {
		return 0, 0, time.Time{}, time.Time{}, false, false
	}
	if a.statsCache != nil {
		a.statsMu.Lock()
		e, found := a.statsCache[id]
		a.statsMu.Unlock()
		if found && !e.fetchedAt.IsZero() {
			return e.stats.FiveHour.Utilization, e.stats.SevenDay.Utilization, e.stats.FiveHour.ResetsAt, e.stats.SevenDay.ResetsAt, false, true
		}
	}
	samples, err := usage.LoadHistory(a.historyPath(id))
	if err != nil || len(samples) == 0 {
		return 0, 0, time.Time{}, time.Time{}, false, false
	}
	last := samples[len(samples)-1]
	return last.FiveHour, last.SevenDay, last.Time, last.Time, true, true
}

func splitSessions(list []agents.Session) (live, done []agents.Session) {
	for _, s := range list {
		if s.State == agents.Done {
			done = append(done, s)
			continue
		}
		live = append(live, s)
	}
	return live, done
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func readingPhrase(t time.Time, fromHistory bool) string {
	if t.IsZero() {
		return "last reading"
	}
	if fromHistory {
		return "as of " + t.Local().Format("3:04pm")
	}
	return "resets " + t.Local().Format("Mon 3pm")
}

// panelDrag is a transparent strip that moves the borderless panel window
// when dragged.
type panelDrag struct {
	widget.BaseWidget
	dragging bool
}

func (d *panelDrag) CreateRenderer() fyne.WidgetRenderer {
	d.ExtendBaseWidget(d)
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (d *panelDrag) Dragged(*fyne.DragEvent) {
	if !d.dragging {
		d.dragging = true
		panelDragBegin(panelTitle)
		return
	}
	panelDragMove(panelTitle)
}

func (d *panelDrag) DragEnd() { d.dragging = false }
