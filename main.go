// Command cd-switcher is a menu-bar / system-tray app that switches the logged-in
// account of Claude Desktop. Switch mode snapshots each account and swaps one
// live window. Side-by-side mode opens each account in its own window.
//
// The UI is built on Fyne: a single event loop owns both the system-tray menu
// and any windows (e.g. Settings). Account operations run on one worker
// goroutine so file mutations never overlap and the UI thread never blocks.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/ncruces/zenity"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/dockicon"
	"github.com/lavluda/cd-switcher/internal/hotkey"
	"github.com/lavluda/cd-switcher/internal/icon"
	"github.com/lavluda/cd-switcher/internal/login"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/secret"
	"github.com/lavluda/cd-switcher/internal/switcher"
	"github.com/lavluda/cd-switcher/internal/uitheme"
	"github.com/lavluda/cd-switcher/internal/usage"
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

	usageClient *usage.Client

	// statsMu guards statsCache. It is deliberately separate from mu: usage
	// fetches make network calls and must never be held up by (or hold up)
	// the file-mutation lock.
	statsMu    sync.Mutex
	statsCache map[string]statEntry

	// alertMu guards alerted. A limit notification fires once per account,
	// window, and reset time, then stays quiet until usage drops back under
	// the threshold.
	alertMu sync.Mutex
	alerted map[alertKey]struct{}
}

type alertKey struct {
	id, window string
	resetUnix  int64
}

var a = &app{}

// statEntry is one profile's last-known usage stats. A failed refresh only
// overwrites err, so the UI can keep showing a stale-but-real value instead
// of blanking out.
type statEntry struct {
	stats     usage.Stats
	plan      string
	tier      string
	fetchedAt time.Time // zero => never succeeded => render "—"
	err       error     // last attempt's outcome; nil if it succeeded
}

func main() {
	a.fyneApp = fyneapp.New()
	// Restyles the Settings window only — the system tray menu and zenity
	// dialogs are native OS chrome and don't go through Fyne's theme.
	a.fyneApp.Settings().SetTheme(uitheme.New())

	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		fatalDialog("System tray is not supported on this platform.")
	}
	a.desk = desk

	a.setup()
	// Menu-bar-only app: no Dock icon. Must run right before Run(): Fyne's
	// GLFW/Cocoa init sets the activation policy to Regular itself — "in
	// case we are unbundled, make us a proper UI application" — so hiding it
	// has to happen after that init, not before. The release .app bundle
	// also sets LSUIElement in Info.plist, but a plain dev binary has no
	// bundle for that to live in.
	dockicon.Hide()
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

	a.usageClient = usage.NewClient()

	if a.cfg.LaunchAtLogin && login.Supported() {
		if err := login.Enable(); err != nil {
			logf("launch at login: %v", err)
		}
	}
	if err := hotkey.Listen(func() { a.runOp(a.doCycle) }); err != nil {
		logf("hotkey: %v", err)
	}

	a.buildAndSetMenu()
	go a.firstRunIfNeeded()
	go a.startStatsLoop()
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
	parallel := a.cfg.IsParallel()
	a.mu.Unlock()

	running := map[string]bool{}
	if parallel {
		if dirs, err := claude.RunningDataDirs(); err == nil {
			for _, dir := range dirs {
				running[dir] = true
			}
		}
	}

	status := fyne.NewMenuItem("", nil)
	status.Disabled = true
	switch {
	case busy:
		status.Label = "⏳ Working…"
	case pending:
		status.Label = "Waiting for new login…"
	case parallel:
		status.Label = "Side by side"
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
		if parallel {
			dir, _ := filepath.Abs(a.store.ProfileDir(id))
			it.Checked = running[filepath.Clean(dir)]
		} else {
			it.Checked = hasActive && id == active.ID
		}

		items = append(items, it)
		for _, line := range a.statLinesFor(id) {
			statLine := fyne.NewMenuItem(line, nil)
			statLine.Disabled = true
			items = append(items, statLine)
		}
	}
	if item, ok := a.freestMenuItem(profiles, active.ID, hasActive, parallel); ok {
		items = append(items, item)
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
		w := a.fyneApp.NewWindow("Settings")
		w.Resize(fyne.NewSize(720, 760))
		// Closing the window must not quit the app (the tray lives on).
		w.SetCloseIntercept(w.Hide)
		a.settingsWin = w
	}
	a.settingsWin.SetContent(a.settingsContent())
	a.settingsWin.Show()
	a.settingsWin.RequestFocus()
	go a.refreshStats()
}

// statsInterval is the background usage-stats poll cadence. The endpoint is
// undocumented, so this is kept infrequent.
const statsInterval = 5 * time.Minute

// statsMinAge skips refetching a profile whose cached stats are still this
// fresh, so opening Settings repeatedly doesn't spam the endpoint.
const statsMinAge = 60 * time.Second

// restorePollInterval is how often side-by-side windows are checked for having
// vanished.
// startStatsLoop fetches usage stats once immediately, then on a timer, for
// as long as the app runs. It never touches a.ops/opWorker (reserved for
// file-mutating account operations) and never blocks the UI thread.
func (a *app) startStatsLoop() {
	a.refreshStats()
	for range time.Tick(statsInterval) {
		a.refreshStats()
	}
}

// refreshStats fetches usage stats for every profile not refreshed within
// statsMinAge, concurrently, then repaints the menu/settings window once.
func (a *app) refreshStats() {
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	a.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range profiles {
		a.statsMu.Lock()
		entry, ok := a.statsCache[p.ID]
		a.statsMu.Unlock()
		if ok && time.Since(entry.fetchedAt) < statsMinAge {
			continue
		}

		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			a.refreshOneStat(id)
		}(p.ID)
	}
	wg.Wait()
	a.refresh()
}

// refreshOneStat decrypts one profile's cached credential and fetches its
// usage stats. Both steps are best-effort: any failure is stored as "stats
// unavailable" rather than surfaced as an app error, since inactive profiles'
// tokens may simply have expired.
func (a *app) refreshOneStat(id string) {
	dir := a.store.ProfileDir(id)
	cred, err := secret.SessionCredential(dir)
	if err != nil {
		logf("stats: decrypt failed for %q: %v", id, err)
		a.setStatEntry(id, statEntry{err: err})
		return
	}
	if !cred.ExpiresAt.IsZero() && time.Until(cred.ExpiresAt) < 2*time.Minute && cred.RefreshToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		refreshed, rerr := a.usageClient.Refresh(ctx, cred.RefreshToken, usage.ScopeFromCacheKey(cred.CacheKey))
		cancel()
		if rerr != nil {
			logf("stats: refresh failed for %q: %v", id, rerr)
		} else {
			cred.Token = refreshed.Access
			cred.ExpiresAt = refreshed.ExpiresAt
			if refreshed.Refresh != "" {
				cred.RefreshToken = refreshed.Refresh
				if werr := secret.UpdateStoredToken(dir, cred); werr != nil {
					logf("stats: could not store refreshed token for %q: %v", id, werr)
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, err := a.usageClient.Fetch(ctx, cred.Token)
	if err != nil {
		logf("stats: fetch failed for %q: %v", id, err)
		a.setStatEntry(id, statEntry{err: err})
		return
	}
	now := time.Now()
	a.setStatEntry(id, statEntry{
		stats: stats, fetchedAt: now,
		plan: cred.SubscriptionType, tier: cred.RateLimitTier,
	})

	sample := usage.Sample{Time: now, FiveHour: stats.FiveHour.Utilization, SevenDay: stats.SevenDay.Utilization}
	if err := usage.AppendHistory(a.historyPath(id), sample); err != nil {
		logf("stats: append history failed for %q: %v", id, err)
	}
	a.maybeWarn(id, stats)
}

// historyPath is where a profile's usage-percentage history is persisted,
// so the Settings-window chart survives app restarts.
func (a *app) historyPath(id string) string {
	p := a.store.HistoryPath(id)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	old := filepath.Join(a.store.ProfileDir(id), "usage_history.json")
	if b, err := os.ReadFile(old); err == nil {
		if mk := os.MkdirAll(filepath.Dir(p), 0o700); mk == nil {
			_ = os.WriteFile(p, b, 0o600)
			return p
		}
		return old
	}
	return p
}

func (a *app) setStatEntry(id string, next statEntry) {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if next.err != nil {
		if prev, ok := a.statsCache[id]; ok && !prev.fetchedAt.IsZero() {
			next.stats, next.fetchedAt = prev.stats, prev.fetchedAt
			next.plan, next.tier = prev.plan, prev.tier
		}
	}
	if a.statsCache == nil {
		a.statsCache = map[string]statEntry{}
	}
	a.statsCache[id] = next
}

// statLinesFor renders a profile's cached usage stats for the tray dropdown,
// one line per limit window (5-hour, then weekly). A single "—" line until the
// first successful fetch.
func (a *app) statLinesFor(id string) []string {
	a.statsMu.Lock()
	e, ok := a.statsCache[id]
	a.statsMu.Unlock()
	if !ok || e.fetchedAt.IsZero() {
		return []string{"usage: —"}
	}
	stale := ""
	if e.err != nil {
		stale = " (stale)"
	}
	fh, sd := e.stats.FiveHour, e.stats.SevenDay
	lines := []string{
		fmt.Sprintf("%.0f%% of session · resets %s%s",
			fh.Utilization, fh.ResetsAt.Local().Format("3:04pm"), stale),
		fmt.Sprintf("%.0f%% of weekly limit · resets %s%s",
			sd.Utilization, sd.ResetsAt.Local().Format("Mon Jan 2"), stale),
	}
	if short := e.stats.Tokens.Short(); short != "" {
		lines = append(lines, "tokens "+short+stale)
	}
	if e.plan != "" {
		plan := e.plan
		if e.tier != "" {
			plan += " · " + e.tier
		}
		lines = append([]string{plan}, lines...)
	}
	if spend := e.stats.ExtraUsage.SpendLabel(); spend != "" {
		lines = append(lines, spend+stale)
	}
	return lines
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
	if a.cfg.IsParallel() {
		return a.doOpenParallel(id)
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

func (a *app) doSetMode(mode string) error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	if mode == profile.ModeParallel {
		err := zenity.Question(
			"Side by side quits the current Claude window and lets you open each account in its own window.\n\nClaude has to accept a separate data directory for this. If a second window does not stay open, switch back.",
			zenity.Title("Side by side"),
			zenity.OKLabel("Use side by side"),
		)
		if err != nil {
			return err
		}
	}
	if err := a.sw.SetMode(a.cfg, mode); err != nil {
		return err
	}
	if mode == profile.ModeParallel {
		_ = zenity.Notify("Side by side is on. Open each account from the menu.")
	}
	return nil
}

func (a *app) doOpenParallel(id string) error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	p, _ := a.cfg.Find(id)
	logf("opening side-by-side window for %q", p.Label)
	dir, _ := filepath.Abs(a.store.ProfileDir(id))
	already, _ := claude.DataDirRunning(dir)
	if err := a.sw.OpenParallel(a.cfg, id); err != nil {
		return err
	}
	if !already {
		_ = zenity.Notify(fmt.Sprintf("Opened %s", p.Label))
	}
	return nil
}

func (a *app) doCloseParallel(id string) error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	return a.sw.CloseParallel(id)
}

func (a *app) doMove(id string, delta int) error {
	if !a.cfg.Move(id, delta) {
		return nil
	}
	return a.store.Save(a.cfg)
}

// freest is the profile with the lowest session utilization among accounts
// that have a successful usage reading.
func (a *app) freest(profiles []profile.Profile) (string, usage.AccountLoad, bool) {
	loads := make([]usage.AccountLoad, len(profiles))
	a.statsMu.Lock()
	for i, p := range profiles {
		e, ok := a.statsCache[p.ID]
		loads[i] = usage.AccountLoad{
			ID:      p.ID,
			Session: e.stats.FiveHour.Utilization,
			Week:    e.stats.SevenDay.Utilization,
			Ready:   ok && !e.fetchedAt.IsZero(),
		}
	}
	a.statsMu.Unlock()
	best, ok := usage.Freest(loads)
	if !ok {
		return "", usage.AccountLoad{}, false
	}
	return best.ID, best, true
}

func (a *app) freestMenuItem(profiles []profile.Profile, activeID string, hasActive, parallel bool) (*fyne.MenuItem, bool) {
	if len(profiles) < 2 {
		return nil, false
	}
	id, load, ok := a.freest(profiles)
	if !ok {
		return nil, false
	}
	label := id
	for _, p := range profiles {
		if p.ID == id {
			label = p.Label
			break
		}
	}
	verb := "Switch to"
	if parallel {
		verb = "Open"
	}
	item := fyne.NewMenuItem(fmt.Sprintf("%s %s · %.0f%% session", verb, label, load.Session), func() {
		a.runOp(a.doUseFreest)
	})
	if !parallel && hasActive && id == activeID {
		item.Label = fmt.Sprintf("Most room: %s · %.0f%% session", label, load.Session)
		item.Disabled = true
	}
	return item, true
}

func (a *app) maybeWarn(id string, stats usage.Stats) {
	a.mu.Lock()
	p, found := a.cfg.Find(id)
	threshold := a.cfg.WarnPercent()
	a.mu.Unlock()
	if !found || threshold <= 0 {
		return
	}
	a.warnWindow(p.Label, id, "session", stats.FiveHour, threshold)
	a.warnWindow(p.Label, id, "weekly", stats.SevenDay, threshold)
}

func (a *app) warnWindow(label, id, window string, w usage.Window, threshold int) {
	if w.Utilization < float64(threshold) {
		a.forgetAlerts(id, window)
		return
	}
	key := alertKey{id: id, window: window, resetUnix: w.ResetsAt.Unix()}
	a.alertMu.Lock()
	if a.alerted == nil {
		a.alerted = map[alertKey]struct{}{}
	}
	if _, seen := a.alerted[key]; seen {
		a.alertMu.Unlock()
		return
	}
	a.alerted[key] = struct{}{}
	a.alertMu.Unlock()

	msg := fmt.Sprintf("%s is at %.0f%% of its %s limit.", label, w.Utilization, window)
	if !w.ResetsAt.IsZero() {
		msg += " Resets " + w.ResetsAt.Local().Format("Mon 3:04pm") + "."
	}
	_ = zenity.Notify(msg)
}

func (a *app) forgetAlerts(id, window string) {
	a.alertMu.Lock()
	for key := range a.alerted {
		if key.id == id && key.window == window {
			delete(a.alerted, key)
		}
	}
	a.alertMu.Unlock()
}

// doUseFreest switches to, or opens, the account with the lowest session
// utilization. The worker already holds a.mu.
func (a *app) doUseFreest() error {
	id, load, ok := a.freest(a.cfg.Profiles)
	if !ok {
		return errors.New("usage stats are not ready yet")
	}
	if a.cfg.IsParallel() {
		return a.doOpenParallel(id)
	}
	if id == a.cfg.ActiveProfile {
		return nil
	}
	logf("using account with the most room: %s at %.0f%% session", load.ID, load.Session)
	return a.doSwitch(id)
}

func (a *app) doSetWarn(percent int) error {
	a.cfg.WarnAt = &percent
	return a.store.Save(a.cfg)
}

func (a *app) doSetLogin(on bool) error {
	var err error
	if on {
		err = login.Enable()
	} else {
		err = login.Disable()
	}
	if err != nil {
		return err
	}
	a.cfg.LaunchAtLogin = on
	return a.store.Save(a.cfg)
}

func (a *app) doCycle() error {
	if len(a.cfg.Profiles) == 0 {
		return nil
	}
	next := 0
	if a.cfg.IsParallel() {
		for i, p := range a.cfg.Profiles {
			dir, _ := filepath.Abs(a.store.ProfileDir(p.ID))
			running, _ := claude.DataDirRunning(dir)
			if running {
				next = (i + 1) % len(a.cfg.Profiles)
			}
		}
		return a.doOpenParallel(a.cfg.Profiles[next].ID)
	}
	for i, p := range a.cfg.Profiles {
		if p.ID == a.cfg.ActiveProfile {
			next = (i + 1) % len(a.cfg.Profiles)
			break
		}
	}
	return a.doSwitch(a.cfg.Profiles[next].ID)
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
