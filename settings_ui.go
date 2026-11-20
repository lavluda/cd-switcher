package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/login"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/terminal"
	"github.com/lavluda/cd-switcher/internal/uitheme"
)

// settingsPage is one page of the panel, found by its title.
type settingsPage struct {
	title string
	build func(*app) fyne.CanvasObject
}

func settingsPages() []settingsPage {
	return []settingsPage{
		{"General", (*app).generalTabContent},
		{"Usage", (*app).usageTabContent},
		{"Accounts", (*app).accountsTabContent},
		{"Switching", (*app).manageProfilesTabContent},
		{"Codex & Copilot", (*app).codexTabContent},
		{"Providers", (*app).providersTabContent},
		{"MCP servers", (*app).mcpTabContent},
		{"Skills", (*app).skillsTabContent},
		{"Instructions", (*app).instructionsTabContent},
		{"Logs", (*app).logsTabContent},
	}
}

// generalTabContent is the "General" page: how accounts open, when to warn
// about limits, startup and updates, and the tools CD-Switcher works with.
// The update check itself lives in the tray menu — see (*app).updateMenuItem
// in main.go — so it works without opening this window.
func (a *app) generalTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	parallel := a.cfg.IsParallel()
	launchAtLogin := a.cfg.LaunchAtLogin
	warnAt := a.cfg.WarnPercent()
	autoCheckUpdates := a.cfg.AutoCheckUpdatesEnabled()
	term := a.cfg.Terminal
	a.mu.Unlock()

	mode := 0
	if parallel {
		mode = 1
	}
	warn := "Off"
	if warnAt == 70 || warnAt == 80 || warnAt == 90 {
		warn = fmt.Sprintf("%d%%", warnAt)
	}
	accounts := prefGroup(c,
		prefRow(c, "Open accounts", "Control-Option-S opens the next account. Control-Option-1 to 9 opens that account.",
			homeSegmented(c, []string{"Switch accounts", "Side by side"}, mode, 12, 12, func(i int) {
				if i == mode {
					return
				}
				m := profile.ModeSwitch
				if i == 1 {
					m = profile.ModeParallel
				}
				a.runOp(func() error { return a.doSetMode(m) })
			})),
		prefRow(c, "Limit alert", "Notify once when a session or weekly limit reaches this.",
			homeSelect(c, warn, []string{"Off", "70%", "80%", "90%"}, true, func(v string) {
				a.runOp(func() error { return a.doSetWarn(pct(v)) }) // "Off" parses to 0
			})),
	)

	var startup []fyne.CanvasObject
	if login.Supported() {
		startup = append(startup, prefRow(c, "Open CD-Switcher at login", "",
			prefToggle(c, launchAtLogin, true, func(on bool) { a.runOp(func() error { return a.doSetLogin(on) }) })))
	}
	startup = append(startup, prefRow(c, "Check for updates automatically", "Checks once a day in the background. Check for Updates in the tray menu runs one on demand.",
		prefToggle(c, autoCheckUpdates, true, func(on bool) { a.runOp(func() error { return a.doSetAutoCheckUpdates(on) }) })))

	apps := terminal.Apps()
	if term == "" {
		term = apps[0]
	}
	hasTool := harness.Installed(harness.Claude, "") || harness.Installed(harness.Codex, "") || harness.Installed(harness.Cursor, "") || harness.Installed(harness.OpenCode, "")
	tools := prefGroup(c,
		prefRow(c, "Agent inbox", "Lists your Claude Code, Codex, Cursor and OpenCode sessions in the menu, and notifies you when one needs your permission or finishes. Adds a small hook to each tool; your other hooks stay as they are.",
			prefToggle(c, inboxInstalled(), hasTool, func(on bool) { a.runOp(func() error { return a.doSetInbox(on) }) })),
		prefRow(c, "Terminal", "Used by Open Claude Code and Open Codex in the menu.",
			homeSelect(c, term, apps, true, func(v string) { a.runOp(func() error { return a.doSetTerminal(v) }) })),
		prefRow(c, "Setup file", "Providers, MCP servers, skills and instructions, to move to another Mac. Accounts and API keys stay here.",
			container.New(layout.NewCustomPaddedHBoxLayout(8),
				homeBtn(c, "Export…", false, func() { a.runOp(a.doExport) }),
				homeBtn(c, "Import…", false, func() { a.runOp(a.doImport) }))),
	)

	return prefPage(pageHeader(c, "General", "How accounts open, limit alerts, startup and tools"),
		prefSection(c, "Accounts"), accounts,
		prefSection(c, "Startup and updates"), prefGroup(c, startup...),
		prefSection(c, "Tools"), tools)
}

// divider is a hairline rule between rows inside the same group card.
func divider() fyne.CanvasObject {
	r := canvas.NewRectangle(uitheme.CurrentGridLine())
	r.SetMinSize(fyne.NewSize(0, 1))
	return r
}

// thinMeter is a short rounded utilization bar, in the accent color unless
// fill is set.
type thinMeter struct {
	widget.BaseWidget
	frac float32
	fill color.Color
	h    float32 // bar height; 6 when zero
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

func (m *thinMeter) MinSize() fyne.Size {
	if m.h > 0 {
		return fyne.NewSize(80, m.h)
	}
	return fyne.NewSize(80, 6)
}

func (m *thinMeter) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(uitheme.CurrentGridLine())
	track.CornerRadius = m.MinSize().Height / 2
	fill := canvas.NewRectangle(m.fillColor())
	fill.CornerRadius = m.MinSize().Height / 2
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
