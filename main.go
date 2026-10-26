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
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
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
	"github.com/lavluda/cd-switcher/internal/updatecheck"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z" (the
// release workflow does this for every published binary). A plain "go build"
// leaves it at "dev", which the tray shows as-is without ever claiming
// to be behind the latest release.
var version = "dev"

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

	// updateChecking marks a GitHub release check in flight, so the Updates
	// tab's button can show "Checking…" and a second click (or the daily
	// background loop firing mid-check) is a no-op instead of overlapping
	// requests racing to write a.cfg.
	updateChecking bool

	fyneApp     fyne.App
	desk        desktop.App
	menu        *fyne.Menu
	settingsWin fyne.Window
	// settingsTabIndex is the Settings window's currently selected tab.
	// settingsContent() rebuilds the whole tab widget on every refresh (a
	// stats poll, a switch, a toggle), so this is what keeps the window on
	// the tab the user is looking at instead of snapping back to the first
	// one each time. UI-thread only, like settingsWin.
	settingsTabIndex int

	normalIcon fyne.Resource
	spin       []fyne.Resource // spinner animation frames

	// restIcon is the tray icon to show whenever the spinner is not running:
	// normalIcon, or (with Show Multiple Profiles on) the composite swatch
	// strip. Only ever set from buildAndSetMenu, which always runs on the UI
	// thread, and read by runIconAnim's own stop handler on that same thread.
	restIcon fyne.Resource

	// animMu guards animCount/animStop. Account operations and background
	// stats syncs can both want the spinner at once; a refcount keeps a
	// single ticker running for as long as any of them is active, instead of
	// racing multiple tickers over SetSystemTrayIcon.
	animMu    sync.Mutex
	animCount int
	animStop  chan struct{}

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
	a.restIcon = a.normalIcon
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
	go func() {
		a.firstRunAskLoginIfNeeded()
		a.firstRunIfNeeded()
	}()
	go a.startStatsLoop()
	go a.startUpdateCheckLoop()
	go a.watchParallelWindows()
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
	showMultiple := a.cfg.ShowMultipleProfiles
	a.mu.Unlock()

	running := map[string]bool{}
	if parallel {
		if dirs, err := claude.RunningDataDirs(); err == nil {
			for _, dir := range dirs {
				running[dir] = true
			}
		}
	}

	a.updateTrayIcon(profiles, active.ID, hasActive, showMultiple)

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

	for i, p := range profiles {
		id := p.ID
		it := fyne.NewMenuItem(p.Label, func() {
			a.runOp(func() error { return a.doSwitch(id) })
		})
		it.Icon = fyne.NewStaticResource(fmt.Sprintf("swatch%d.png", i), icon.Swatch(i))
		if parallel {
			dir, _ := filepath.Abs(a.store.ProfileDir(id))
			it.Checked = running[filepath.Clean(dir)]
		} else {
			it.Checked = hasActive && id == active.ID
		}

		items = append(items, it)
		items = append(items, a.statLinesFor(id)...)
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
		add := fyne.NewMenuItem("Add account…", func() {
			a.runOp(a.doAddAccount)
		})
		add.Icon = theme.ContentAddIcon()
		items = append(items, add)
	}

	settings := fyne.NewMenuItem("Settings…", a.openSettings)
	settings.Icon = theme.SettingsIcon()
	update := a.updateMenuItem()
	update.Icon = theme.DownloadIcon()
	quit := fyne.NewMenuItem("Quit", func() { a.fyneApp.Quit() })
	quit.Icon = theme.LogoutIcon()
	items = append(items,
		fyne.NewMenuItemSeparator(),
		settings,
		update,
		fyne.NewMenuItemSeparator(),
		quit,
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
		w.Resize(fyne.NewSize(780, 760))
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
const restorePollInterval = 5 * time.Second

// watchParallelWindows reopens side-by-side windows that Claude closed to
// install an update. Claude's updater relaunches without --user-data-dir, so
// the windows would otherwise stay gone. Windows the user closed themselves are
// left alone: only an update restart in Claude's log triggers a reopen.
func (a *app) watchParallelWindows() {
	r := switcher.NewRestorer()
	for range time.Tick(restorePollInterval) {
		a.mu.Lock()
		parallel := a.cfg.IsParallel()
		var ids []string
		for _, p := range a.cfg.Profiles {
			ids = append(ids, p.ID)
		}
		a.mu.Unlock()
		if !parallel {
			r = switcher.NewRestorer()
			continue
		}

		running := map[string]bool{}
		dirs, err := claude.RunningDataDirs()
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			for _, id := range ids {
				if abs, err := filepath.Abs(a.store.ProfileDir(id)); err == nil && filepath.Clean(abs) == dir {
					running[id] = true
				}
			}
		}

		updateQuit, _ := claude.LastUpdateQuit()
		now := time.Now()
		r.Observe(now, ids, running, updateQuit)

		due := r.Due(now)
		if len(due) == 0 {
			continue
		}
		// One operation for all windows: runOp drops a second request while one
		// is queued, and the worker keeps file changes from overlapping.
		select {
		case a.ops <- func() error { return a.doRestoreParallel(due) }:
			r.Done(due)
		default: // worker is busy; try again next tick
		}
	}
}

// doRestoreParallel reopens windows that an update restart closed.
func (a *app) doRestoreParallel(ids []string) error {
	if a.sw == nil {
		return errors.New("Claude Desktop data directory not found")
	}
	for _, id := range ids {
		p, ok := a.cfg.Find(id)
		if !ok {
			continue
		}
		logf("reopening side-by-side window for %q after a Claude update", p.Label)
		if err := a.sw.OpenParallel(a.cfg, id); err != nil {
			return err
		}
	}
	return nil
}

// startStatsLoop fetches usage stats once immediately, then on a timer, for
// as long as the app runs. It never touches a.ops/opWorker (reserved for
// file-mutating account operations) and never blocks the UI thread.
func (a *app) startStatsLoop() {
	a.refreshStats()
	for range time.Tick(statsInterval) {
		a.refreshStats()
	}
}

// updateCheckInterval is how often the background loop checks GitHub for a
// new release when Automatic Updates is on.
const updateCheckInterval = 24 * time.Hour

// startUpdateCheckLoop checks for a new release once at startup (if the last
// check is more than a day old) and then once a day for as long as the app
// runs, as long as Automatic Updates stays enabled. It never touches
// a.ops/opWorker, same reasoning as startStatsLoop: a GitHub request must
// never be able to stall an account switch or vice versa.
func (a *app) startUpdateCheckLoop() {
	a.mu.Lock()
	due := a.cfg.AutoCheckUpdatesEnabled() && time.Since(a.cfg.LastUpdateCheck) > updateCheckInterval
	a.mu.Unlock()
	if due {
		a.checkForUpdates()
	}
	for range time.Tick(updateCheckInterval) {
		a.mu.Lock()
		enabled := a.cfg.AutoCheckUpdatesEnabled()
		a.mu.Unlock()
		if enabled {
			a.checkForUpdates()
		}
	}
}

// checkForUpdates fetches the latest GitHub release and caches it in a.cfg,
// then repaints the tray menu. A second call while one is already in
// flight (a user click racing the daily timer) is dropped rather than
// racing two writes to a.cfg.
func (a *app) checkForUpdates() {
	a.mu.Lock()
	if a.updateChecking {
		a.mu.Unlock()
		return
	}
	a.updateChecking = true
	a.mu.Unlock()
	a.refresh()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rel, err := updatecheck.Latest(ctx)
	cancel()
	if err != nil {
		logf("update check failed: %v", err)
	}

	a.mu.Lock()
	a.updateChecking = false
	a.cfg.LastUpdateCheck = time.Now()
	if err == nil {
		a.cfg.LatestVersion = rel.Version
		a.cfg.LatestVersionURL = rel.URL
	}
	saveErr := a.store.Save(a.cfg)
	a.mu.Unlock()
	if saveErr != nil {
		logf("update check: save config: %v", saveErr)
	}
	a.refresh()
}

// updateMenuItem is the tray menu's update row — this is the only UI for
// checking updates; there is deliberately no Settings tab for it, so it works
// without opening that window. Three states: a disabled "Checking…" row while
// a check is running, "Update available: vX" opening the release page once
// one is found, or a plain "Check for Updates" action otherwise.
func (a *app) updateMenuItem() *fyne.MenuItem {
	a.mu.Lock()
	checking := a.updateChecking
	latest := a.cfg.LatestVersion
	latestURL := a.cfg.LatestVersionURL
	a.mu.Unlock()

	switch {
	case checking:
		it := fyne.NewMenuItem("Checking for updates…", nil)
		it.Disabled = true
		return it
	case latest != "" && updatecheck.IsNewer(version, latest):
		return fyne.NewMenuItem(fmt.Sprintf("Update available: %s", latest), func() {
			u, err := url.Parse(latestURL)
			if err != nil {
				logf("update menu: bad release URL %q: %v", latestURL, err)
				return
			}
			if err := a.fyneApp.OpenURL(u); err != nil {
				logf("update menu: open release URL: %v", err)
			}
		})
	default:
		return fyne.NewMenuItem("Check for Updates", func() { go a.checkForUpdates() })
	}
}

// refreshStats fetches usage stats for every profile not refreshed within
// statsMinAge, concurrently, then repaints the menu/settings window once.
func (a *app) refreshStats() {
	a.mu.Lock()
	profiles := append([]profile.Profile(nil), a.cfg.Profiles...)
	a.mu.Unlock()

	var pending []string
	for _, p := range profiles {
		a.statsMu.Lock()
		entry, ok := a.statsCache[p.ID]
		a.statsMu.Unlock()
		if ok && time.Since(entry.fetchedAt) < statsMinAge {
			continue
		}
		pending = append(pending, p.ID)
	}
	if len(pending) == 0 {
		return
	}

	// Spins the tray icon for as long as any profile is still syncing.
	start := time.Now()
	a.beginAnim()

	var wg sync.WaitGroup
	for _, id := range pending {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			a.refreshOneStat(id)
		}(id)
	}
	wg.Wait()

	sleepRemaining(start)
	a.endAnim()
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
	a.maybeAutoSwitch(id, stats)
}

// maybeAutoSwitch switches away from the active profile once its session
// usage reaches 100%, picking whichever other profile has the most room left.
// It only applies to the active profile in switch mode (side-by-side already
// keeps every account open) and fires once per reset window, the same
// debounce scheme maybeWarn uses for limit notifications.
func (a *app) maybeAutoSwitch(id string, stats usage.Stats) {
	a.mu.Lock()
	enabled := a.cfg.AutoSwitchOnLimit
	isActive := id == a.cfg.ActiveProfile
	parallel := a.cfg.IsParallel()
	a.mu.Unlock()
	if !enabled || !isActive || parallel {
		return
	}

	if stats.FiveHour.Utilization < 100 {
		a.forgetAlerts(id, "autoswitch")
		return
	}

	key := alertKey{id: id, window: "autoswitch", resetUnix: stats.FiveHour.ResetsAt.Unix()}
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

	logf("auto-switch: %q hit 100%% session usage, switching to the most available profile", id)
	a.runOp(a.doUseFreest)
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

// statLinesFor renders a profile's cached usage stats for the tray dropdown.
// The session and weekly rows carry a coloured ring gauge and stay enabled
// (clicking opens Settings) so macOS doesn't grey the colour out; the plan,
// token and spend rows are dimmed secondary text. A single "—" row until the
// first successful fetch.
func (a *app) statLinesFor(id string) []*fyne.MenuItem {
	a.statsMu.Lock()
	e, ok := a.statsCache[id]
	a.statsMu.Unlock()
	if !ok || e.fetchedAt.IsZero() {
		return []*fyne.MenuItem{dimItem("usage: —")}
	}
	stale := ""
	if e.err != nil {
		stale = " (stale)"
	}
	var items []*fyne.MenuItem
	if e.plan != "" {
		plan := e.plan
		if e.tier != "" {
			plan += " · " + e.tier
		}
		items = append(items, dimItem(plan))
	}
	fh, sd := e.stats.FiveHour, e.stats.SevenDay
	items = append(items,
		gaugeItem(fmt.Sprintf("Session %.0f%% · resets %s%s",
			fh.Utilization, fh.ResetsAt.Local().Format("3:04pm"), stale), fh.Utilization, a.openSettings),
		gaugeItem(fmt.Sprintf("Weekly %.0f%% · resets %s%s",
			sd.Utilization, sd.ResetsAt.Local().Format("Mon Jan 2"), stale), sd.Utilization, a.openSettings),
	)
	if short := e.stats.Tokens.Short(); short != "" {
		items = append(items, dimItem("tokens "+short+stale))
	}
	if spend := e.stats.ExtraUsage.SpendLabel(); spend != "" {
		items = append(items, dimItem(spend+stale))
	}
	return items
}

func dimItem(label string) *fyne.MenuItem {
	it := fyne.NewMenuItem(label, nil)
	it.Disabled = true
	return it
}

func gaugeItem(label string, pct float64, action func()) *fyne.MenuItem {
	it := fyne.NewMenuItem(label, action)
	it.Icon = fyne.NewStaticResource(fmt.Sprintf("gauge%.0f.png", pct), icon.Gauge(pct))
	return it
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

// minSpinDuration is the shortest time the tray icon spins for, even when the
// underlying work (a local file copy, a quickly-rejected network call)
// finishes faster than that. Without this floor, fast operations clear the
// spinner before a single frame is visible.
const minSpinDuration = 700 * time.Millisecond

// execOp runs one operation: shows the spinner, runs the work under the lock
// (panic-contained), then clears the spinner and refreshes the menu.
func (a *app) execOp(fn func() error) {
	a.setBusy(true)
	start := time.Now()
	a.beginAnim()

	err := a.safeRun(fn)

	sleepRemaining(start)
	a.endAnim()
	a.setBusy(false) // also refreshes the menu (new active profile, etc.)

	if err != nil && !errors.Is(err, zenity.ErrCanceled) {
		logf("operation failed: %v", err)
		notifyErr("Operation failed", err)
	}
}

// sleepRemaining blocks until minSpinDuration has passed since start.
func sleepRemaining(start time.Time) {
	if d := minSpinDuration - time.Since(start); d > 0 {
		time.Sleep(d)
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

// beginAnim marks one more reason the tray icon should be spinning —
// switching accounts, adding/saving one, or a stats sync all call this. The
// ticker starts on the first caller and keeps running until the matching
// endAnim calls bring the count back to zero.
func (a *app) beginAnim() {
	a.animMu.Lock()
	defer a.animMu.Unlock()
	a.animCount++
	if a.animCount == 1 {
		a.animStop = make(chan struct{})
		go a.runIconAnim(a.animStop)
	}
}

// endAnim releases one beginAnim call, stopping the ticker once nothing else
// still needs it.
func (a *app) endAnim() {
	a.animMu.Lock()
	defer a.animMu.Unlock()
	a.animCount--
	if a.animCount <= 0 {
		a.animCount = 0
		close(a.animStop)
		a.animStop = nil
	}
}

// runIconAnim cycles the tray icon through the spinner frames until stop is
// closed, giving at-a-glance "working" feedback in the menu bar (Fyne trays
// show an icon, not a text title).
func (a *app) runIconAnim(stop chan struct{}) {
	t := time.NewTicker(110 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-stop:
			fyne.Do(func() { a.desk.SetSystemTrayIcon(a.restIcon) })
			return
		case <-t.C:
			frame := a.spin[i%len(a.spin)]
			fyne.Do(func() { a.desk.SetSystemTrayIcon(frame) })
		}
	}
}

// updateTrayIcon sets the tray icon for the current profile list: the plain
// app icon normally, or (when Show Multiple Profiles is on and there is more
// than one profile) a composite strip with one swatch per profile and a ring
// around the active one. Skipped while the spin animation owns the icon; its
// own stop handler picks up a.restIcon once it finishes.
func (a *app) updateTrayIcon(profiles []profile.Profile, activeID string, hasActive, showMultiple bool) {
	next := a.normalIcon
	if showMultiple && len(profiles) > 1 {
		activeIndex := -1
		if hasActive {
			for i, p := range profiles {
				if p.ID == activeID {
					activeIndex = i
					break
				}
			}
		}
		data := icon.MultiProfileIcon(len(profiles), activeIndex)
		next = fyne.NewStaticResource(fmt.Sprintf("cd-switcher-multi-%d-%d", len(profiles), activeIndex), data)
	}
	a.restIcon = next

	a.animMu.Lock()
	spinning := a.animCount > 0
	a.animMu.Unlock()
	if !spinning {
		a.desk.SetSystemTrayIcon(next)
	}
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
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	// Check cached usage against the new threshold now rather than at the next
	// poll. In a goroutine because maybeWarn takes a.mu, which runOp holds.
	go a.recheckWarnings()
	return nil
}

func (a *app) recheckWarnings() {
	a.statsMu.Lock()
	cached := make(map[string]usage.Stats, len(a.statsCache))
	for id, e := range a.statsCache {
		if !e.fetchedAt.IsZero() {
			cached[id] = e.stats
		}
	}
	a.statsMu.Unlock()
	for id, stats := range cached {
		a.maybeWarn(id, stats)
	}
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
	a.cfg.LaunchAtLoginAsked = true
	return a.store.Save(a.cfg)
}

func (a *app) doSetShowMultipleProfiles(on bool) error {
	a.cfg.ShowMultipleProfiles = on
	return a.store.Save(a.cfg)
}

func (a *app) doSetAutoSwitch(on bool) error {
	a.cfg.AutoSwitchOnLimit = on
	return a.store.Save(a.cfg)
}

func (a *app) doSetAutoCheckUpdates(on bool) error {
	a.cfg.AutoCheckUpdates = &on
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

// firstRunAskLoginIfNeeded asks, once, whether to open CD-Switcher
// automatically at login. The Settings checkbox can change the answer later;
// this just keeps the app from nagging about it on every later launch.
func (a *app) firstRunAskLoginIfNeeded() {
	if !login.Supported() {
		return
	}
	a.mu.Lock()
	asked := a.cfg.LaunchAtLoginAsked
	a.mu.Unlock()
	if asked {
		return
	}

	err := zenity.Question(
		"Open CD-Switcher automatically when you log in?\n\nYou can change this later in Settings.",
		zenity.Title("CD-Switcher setup"),
		zenity.OKLabel("Open at login"),
		zenity.CancelLabel("Not now"),
	)
	if err != nil && !errors.Is(err, zenity.ErrCanceled) {
		notifyErr("Setup failed", err)
		return
	}
	enable := err == nil
	a.runOp(func() error { return a.doSetLogin(enable) })
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
