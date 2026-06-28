// Command cd-switcher is a menu-bar / system-tray app that switches the logged-in
// account of Claude Desktop. It snapshots each account's data directory into a
// named profile and swaps profiles in and out, relaunching Desktop signed into
// the chosen account.
//
// The UI is built on Fyne: a single event loop owns both the system-tray menu
// and any windows (e.g. Settings). Account operations run on one worker
// goroutine so file mutations never overlap and the UI thread never blocks.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/ncruces/zenity"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/icon"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/switcher"
)

type app struct {
	mu    sync.Mutex // guards cfg / pendingLogin / busy
	store *profile.Store
	sw    *switcher.Switcher
	cfg   *profile.Config

	// ops carries account operations to the single worker goroutine. Capacity 1
	// means at most one operation can be queued behind the running one; further
	// clicks while busy are coalesced (dropped) rather than stacked.
	ops chan func() error

	pendingLogin bool
	busy         bool // an operation is in progress (drives the status row)

	fyneApp     fyne.App
	desk        desktop.App
	menu        *fyne.Menu
	settingsWin fyne.Window

	normalIcon fyne.Resource
	spin       []fyne.Resource // spinner animation frames
}

var a = &app{}

func main() {
	a.fyneApp = fyneapp.New()

	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		fatalDialog("System tray is not supported on this platform.")
	}
	a.desk = desk

	a.setup()
	a.fyneApp.Run()
}

// setup runs on the main goroutine before the event loop starts. Non-UI state
// is initialized here; the initial menu is built directly (allowed pre-Run).
func (a *app) setup() {
	store, err := profile.NewStore()
	if err != nil {
		fatalDialog("Could not open switcher config: " + err.Error())
	}
	a.store = store
	initLog(store.Root())

	a.normalIcon = fyne.NewStaticResource("cd-switcher", icon.Data())
	a.spin = spinnerResources()
	a.fyneApp.SetIcon(a.normalIcon)
	a.desk.SetSystemTrayIcon(a.normalIcon)

	// Single worker drains the ops queue so menu callbacks never block the UI.
	a.ops = make(chan func() error, 1)
	go a.opWorker()

	a.cfg, err = store.Load()
	if err != nil {
		fatalDialog("Could not read config: " + err.Error())
	}

	a.sw, err = switcher.New(store)
	if err != nil {
		// Claude not installed yet: keep running but warn.
		notifyErr("Claude Desktop not found", err)
	}

	a.buildAndSetMenu()
	go a.firstRunIfNeeded()
}

// buildAndSetMenu rebuilds the tray menu from current state and installs it.
// It must run on the UI thread (called directly pre-Run, via fyne.Do after).
func (a *app) buildAndSetMenu() {
	// Snapshot everything the menu needs under the lock: the worker goroutine
	// mutates a.cfg's slice/fields concurrently, so we must not read them after
	// unlocking.
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	active, hasActive := a.cfg.Active()
	pending := a.pendingLogin
	busy := a.busy
	a.mu.Unlock()

	status := fyne.NewMenuItem("", nil)
	status.Disabled = true
	switch {
	case busy:
		status.Label = "⏳ Working…"
	case pending:
		status.Label = "Waiting for new login…"
	case hasActive:
		status.Label = "Active: " + active.Label
	default:
		status.Label = "No account active"
	}

	items := []*fyne.MenuItem{status, fyne.NewMenuItemSeparator()}

	for _, p := range profiles {
		id := p.ID
		it := fyne.NewMenuItem(p.Label, func() {
			a.runOp(func() error { return a.doSwitch(id) })
		})
		it.Checked = hasActive && id == active.ID
		items = append(items, it)
	}
	items = append(items, fyne.NewMenuItemSeparator())

	if pending {
		items = append(items, fyne.NewMenuItem("✓ Save this account", func() {
			a.runOp(a.doSaveAccount)
		}))
	} else {
		items = append(items, fyne.NewMenuItem("Add account…", func() {
			a.runOp(a.doAddAccount)
		}))
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Settings…", a.openSettings),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { a.fyneApp.Quit() }),
	)

	a.menu = fyne.NewMenu("CD-Switcher", items...)
	a.desk.SetSystemTrayMenu(a.menu)
}

// refresh rebuilds the menu (and the settings window, if open) on the UI thread
// from a background goroutine.
func (a *app) refresh() {
	fyne.Do(func() {
		a.buildAndSetMenu()
		if a.settingsWin != nil {
			a.settingsWin.SetContent(a.settingsContent())
		}
	})
}

// openSettings shows the (reused) settings window. Runs on the UI thread as a
// menu action.
func (a *app) openSettings() {
	if a.settingsWin == nil {
		w := a.fyneApp.NewWindow("CD-Switcher Settings")
		w.Resize(fyne.NewSize(460, 360))
		// Closing the window must not quit the app (the tray lives on).
		w.SetCloseIntercept(w.Hide)
		a.settingsWin = w
	}
	a.settingsWin.SetContent(a.settingsContent())
	a.settingsWin.Show()
	a.settingsWin.RequestFocus()
}

// settingsContent builds the settings window body: one row per account with
// rename/remove actions. Reads state under the lock; runs on the UI thread.
func (a *app) settingsContent() fyne.CanvasObject {
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	activeID := a.cfg.ActiveProfile
	a.mu.Unlock()

	header := widget.NewLabelWithStyle("Accounts", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	rows := container.NewVBox(header)

	if len(profiles) == 0 {
		rows.Add(widget.NewLabel("No accounts yet — use “Add account…” from the menu."))
	}
	for _, p := range profiles {
		id := p.ID
		name := p.Label
		if activeID == id {
			name = "● " + name + "   (active)"
		}
		renameBtn := widget.NewButton("Rename", func() {
			a.runOp(func() error { return a.doRename(id) })
		})
		removeBtn := widget.NewButton("Remove", func() {
			a.runOp(func() error { return a.doRemove(id) })
		})
		actions := container.NewHBox(renameBtn, removeBtn)
		rows.Add(container.NewBorder(nil, nil, widget.NewLabel(name), actions))
	}

	note := widget.NewLabel("Usage & token stats coming next.")
	note.Importance = widget.LowImportance
	return container.NewBorder(nil, note, nil, nil, container.NewVScroll(rows))
}

// runOp hands an account operation to the worker and returns immediately so the
// UI thread is never blocked by a switch (quit/launch can take seconds). If an
// operation is already queued, the extra click is coalesced away.
func (a *app) runOp(fn func() error) {
	select {
	case a.ops <- fn:
	default:
		logf("ignoring click: an operation is already in progress")
		_ = zenity.Notify("Busy — finishing the previous action…")
	}
}

// opWorker runs queued operations one at a time, off the UI thread.
func (a *app) opWorker() {
	for fn := range a.ops {
		a.execOp(fn)
	}
}

// execOp runs one operation: shows the spinner, runs the work under the lock
// (panic-contained), then clears the spinner and refreshes the menu.
func (a *app) execOp(fn func() error) {
	a.setBusy(true)
	stopAnim := a.animateIcon()

	err := a.safeRun(fn)

	close(stopAnim)
	a.setBusy(false) // also refreshes the menu (new active profile, etc.)

	if err != nil && !errors.Is(err, zenity.ErrCanceled) {
		logf("operation failed: %v", err)
		notifyErr("Operation failed", err)
	}
}

// safeRun runs fn under the state lock, converting a panic into an error so one
// bad operation can't wedge the lock or take down the tray.
func (a *app) safeRun(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logf("operation panicked: %v", r)
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	return fn()
}

func (a *app) setBusy(b bool) {
	a.mu.Lock()
	a.busy = b
	a.mu.Unlock()
	a.refresh()
}

// animateIcon cycles the tray icon through the spinner frames until the
// returned channel is closed, giving at-a-glance "working" feedback in the
// menu bar (Fyne trays show an icon, not a text title).
func (a *app) animateIcon() chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(110 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				fyne.Do(func() { a.desk.SetSystemTrayIcon(a.normalIcon) })
				return
			case <-t.C:
				frame := a.spin[i%len(a.spin)]
				fyne.Do(func() { a.desk.SetSystemTrayIcon(frame) })
			}
		}
	}()
	return stop
}

// --- operations (run with a.mu held, on the worker goroutine) ---

func (a *app) doSwitch(id string) error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	p, _ := a.cfg.Find(id)
	logf("switching to %q (id=%s)", p.Label, id)
	if err := a.sw.SwitchTo(a.cfg, id, true); err != nil {
		return err
	}
	logf("switched to %q", p.Label)
	_ = zenity.Notify(fmt.Sprintf("Switched to %s", p.Label))
	return nil
}

func (a *app) doAddAccount() error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}

	// If we have never captured the current account, do that first so it isn't
	// lost when we clear the live dir for a fresh login.
	if a.cfg.ActiveProfile == "" && len(a.cfg.Profiles) == 0 {
		if err := a.captureCurrent("Personal"); err != nil {
			return err
		}
	}

	err := zenity.Question(
		"This will quit Claude Desktop, save your current account, and open a fresh login screen.\n\nLog into the other account, then choose “Save this account”.",
		zenity.Title("Add account"),
		zenity.OKLabel("Continue"),
	)
	if err != nil {
		return err // ErrCanceled handled upstream
	}

	if err := a.sw.PrepareCleanLogin(a.cfg); err != nil {
		return err
	}
	a.pendingLogin = true
	return nil
}

func (a *app) doSaveAccount() error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	label, err := zenity.Entry(
		"Name this account:",
		zenity.Title("Save account"),
		zenity.EntryText("Work"),
	)
	if err != nil {
		return err
	}
	if label == "" {
		label = "Account"
	}

	p := profile.Profile{ID: a.cfg.NewID(label), Label: label, CreatedAt: time.Now()}
	if err := a.sw.CaptureAs(a.cfg, p); err != nil {
		return err
	}
	a.pendingLogin = false
	if err := claude.Launch(); err != nil {
		return err
	}
	_ = zenity.Notify(fmt.Sprintf("Saved account: %s", label))
	return nil
}

func (a *app) doRename(id string) error {
	p, ok := a.cfg.Find(id)
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	label, err := zenity.Entry(
		"New name for this account:",
		zenity.Title("Rename account"),
		zenity.EntryText(p.Label),
	)
	if err != nil {
		return err // ErrCanceled handled upstream
	}
	if label == "" || label == p.Label {
		return nil // empty or unchanged: treat as cancel
	}
	a.cfg.Rename(id, label)
	logf("renamed %q -> %q", p.Label, label)
	return a.store.Save(a.cfg)
}

func (a *app) doRemove(id string) error {
	p, ok := a.cfg.Find(id)
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	if len(a.cfg.Profiles) <= 1 {
		_ = zenity.Info(
			"This is your only account profile. Rename it, or add another account before removing this one.",
			zenity.Title("Remove account"),
		)
		return nil
	}
	msg := fmt.Sprintf(
		"Remove the account profile “%s”?\n\nThis deletes CD-Switcher's saved snapshot for it. Your live Claude login is not touched.",
		p.Label,
	)
	if a.cfg.ActiveProfile == id {
		msg += "\n\nThis is the active profile, so CD-Switcher will stop tracking it."
	}
	if err := zenity.Question(msg, zenity.Title("Remove account"), zenity.OKLabel("Remove")); err != nil {
		return err // ErrCanceled handled upstream
	}

	// Update config first so a failed snapshot delete leaves an orphan dir
	// rather than a config that points at a missing snapshot.
	a.cfg.Remove(id)
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	if err := a.store.RemoveSnapshot(id); err != nil {
		logf("remove snapshot %q failed (config already updated): %v", id, err)
	}
	logf("removed profile %q", p.Label)
	return nil
}

// captureCurrent snapshots the currently logged-in account as a new active
// profile, prompting for a label. Quits and relaunches Claude.
func (a *app) captureCurrent(defaultLabel string) error {
	label, err := zenity.Entry(
		"Name the account you're currently signed into:",
		zenity.Title("Capture current account"),
		zenity.EntryText(defaultLabel),
	)
	if err != nil {
		return err
	}
	if label == "" {
		label = defaultLabel
	}
	p := profile.Profile{ID: a.cfg.NewID(label), Label: label, CreatedAt: time.Now()}
	if err := a.sw.CaptureAs(a.cfg, p); err != nil {
		return err
	}
	if err := claude.Launch(); err != nil {
		return err
	}
	_ = zenity.Notify(fmt.Sprintf("Captured account: %s", label))
	return nil
}

// firstRunIfNeeded offers to capture the current account when no profiles exist.
func (a *app) firstRunIfNeeded() {
	a.mu.Lock()
	empty := len(a.cfg.Profiles) == 0
	ready := a.sw != nil
	a.mu.Unlock()
	if !empty || !ready {
		return
	}

	err := zenity.Question(
		"Welcome! Capture the account you're currently signed into in Claude Desktop as your first profile?\n\nClaude will briefly quit and reopen.",
		zenity.Title("CD-Switcher setup"),
		zenity.OKLabel("Capture"),
	)
	if errors.Is(err, zenity.ErrCanceled) {
		return
	}
	if err != nil {
		notifyErr("Setup failed", err)
		return
	}
	a.runOp(func() error { return a.captureCurrent("Personal") })
}

// --- helpers ---

// spinnerResources wraps the icon package's animation frames as Fyne resources.
func spinnerResources() []fyne.Resource {
	frames := icon.SpinnerFrames()
	out := make([]fyne.Resource, len(frames))
	for i, f := range frames {
		out[i] = fyne.NewStaticResource(fmt.Sprintf("cd-switcher-spin-%d", i), f)
	}
	return out
}

// logger writes to <config-dir>/cd-switcher.log so failures that happen while
// the app runs detached from a terminal are diagnosable after the fact.
var logger *log.Logger

func initLog(root string) {
	f, err := os.OpenFile(filepath.Join(root, "cd-switcher.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return // logging is best-effort
	}
	logger = log.New(f, "", log.LstdFlags|log.Lmsgprefix)
	logf("--- cd-switcher started ---")
}

func logf(format string, args ...any) {
	if logger != nil {
		logger.Printf(format, args...)
	}
}

func notifyErr(title string, err error) {
	_ = zenity.Error(err.Error(), zenity.Title(title))
}

func fatalDialog(msg string) {
	_ = zenity.Error(msg, zenity.Title("CD-Switcher"))
	os.Exit(1)
}
