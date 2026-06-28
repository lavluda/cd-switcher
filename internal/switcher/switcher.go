// Package switcher orchestrates account switching: it gates on Claude Desktop
// being fully exited, snapshots the current account, swaps in the target
// account's snapshot, and relaunches the app. It is the single place that
// mutates the live Claude data directory.
package switcher

import (
	"fmt"
	"os"
	"time"

	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/profile"
)

// quitTimeout is how long we wait for Claude to fully exit before aborting.
const quitTimeout = 15 * time.Second

// AppController abstracts controlling the Claude Desktop process so the
// orchestration can be tested without quitting/launching the real app.
type AppController interface {
	Quit(timeout time.Duration) error
	Launch() error
}

// realApp drives the actual Claude Desktop app via the claude package.
type realApp struct{}

func (realApp) Quit(timeout time.Duration) error { return claude.Quit(timeout) }
func (realApp) Launch() error                    { return claude.Launch() }

// Switcher couples the profile store with the live Claude data dir.
type Switcher struct {
	store   *profile.Store
	liveDir string
	app     AppController
}

// New builds a Switcher, resolving the live Claude data directory. It returns
// claude.ErrNotInstalled if Desktop has never run on this machine.
func New(store *profile.Store) (*Switcher, error) {
	live, err := claude.DataDir()
	if err != nil {
		return nil, err
	}
	return newWith(store, live, realApp{}), nil
}

// newWith builds a Switcher with explicit dependencies (used by tests).
func newWith(store *profile.Store, liveDir string, app AppController) *Switcher {
	return &Switcher{store: store, liveDir: liveDir, app: app}
}

// LiveDir is the resolved Claude Desktop data directory.
func (s *Switcher) LiveDir() string { return s.liveDir }

// backupDir is a sibling of the live dir used for rollback during Restore.
func (s *Switcher) backupDir() string {
	return s.liveDir + ".cdswitch-bak"
}

// ensureQuit makes sure Claude is fully exited before any file mutation.
func (s *Switcher) ensureQuit() error {
	if err := s.app.Quit(quitTimeout); err != nil {
		return fmt.Errorf("could not quit Claude Desktop: %w", err)
	}
	return nil
}

// CaptureActive snapshots the current live dir back into the active profile.
// It is a no-op when there is no active profile. Claude must already be quit.
func (s *Switcher) CaptureActive(cfg *profile.Config) error {
	active, ok := cfg.Active()
	if !ok {
		return nil
	}
	return profile.Capture(s.liveDir, s.store.ProfileDir(active.ID))
}

// CaptureAs snapshots the current live dir into a (new or existing) profile and
// records it as active. Quits Claude first. Used by first-run capture and the
// "Save this account" step of add-account.
func (s *Switcher) CaptureAs(cfg *profile.Config, p profile.Profile) error {
	if err := s.ensureQuit(); err != nil {
		return err
	}
	if err := profile.Capture(s.liveDir, s.store.ProfileDir(p.ID)); err != nil {
		return fmt.Errorf("capture snapshot: %w", err)
	}

	if _, exists := cfg.Find(p.ID); !exists {
		cfg.Profiles = append(cfg.Profiles, p)
	}
	cfg.ActiveProfile = p.ID
	return s.store.Save(cfg)
}

// SwitchTo switches the live account to target: quit -> sync active back ->
// swap target in (with rollback on failure) -> relaunch. relaunch may be set
// false by callers that want to leave the app closed.
func (s *Switcher) SwitchTo(cfg *profile.Config, targetID string, relaunch bool) error {
	target, ok := cfg.Find(targetID)
	if !ok {
		return fmt.Errorf("unknown profile %q", targetID)
	}
	if cfg.ActiveProfile == targetID {
		// Already active; just make sure the app is up.
		if relaunch {
			return s.app.Launch()
		}
		return nil
	}

	if err := s.ensureQuit(); err != nil {
		return err
	}

	// Preserve any new local state of the currently-active account.
	if err := s.CaptureActive(cfg); err != nil {
		return fmt.Errorf("sync active profile: %w", err)
	}

	// Swap target snapshot into the live dir, with backup for rollback.
	if err := s.restoreWithRollback(target.ID); err != nil {
		return err
	}

	cfg.ActiveProfile = targetID
	if err := s.store.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if relaunch {
		return s.app.Launch()
	}
	return nil
}

// restoreWithRollback backs up the live dir, restores the target snapshot, and
// undoes the change from the backup if the restore fails partway through.
func (s *Switcher) restoreWithRollback(targetID string) error {
	bak := s.backupDir()
	if err := profile.BackupLive(s.liveDir, bak); err != nil {
		return fmt.Errorf("back up live dir: %w", err)
	}

	if err := profile.Restore(s.store.ProfileDir(targetID), s.liveDir); err != nil {
		// Best-effort rollback; surface the original error either way.
		if rbErr := profile.RollbackLive(bak, s.liveDir); rbErr != nil {
			return fmt.Errorf("restore failed (%v) and rollback failed (%v)", err, rbErr)
		}
		return fmt.Errorf("restore target profile (rolled back): %w", err)
	}

	// Success: discard the backup.
	return os.RemoveAll(bak)
}

// PrepareCleanLogin quits Claude, snapshots the active account, clears the live
// dir to a logged-out state, then relaunches Claude so the user can sign into a
// new account. After they log in, the caller captures it via CaptureAs. Marks
// no profile active while the new login is pending.
func (s *Switcher) PrepareCleanLogin(cfg *profile.Config) error {
	if err := s.ensureQuit(); err != nil {
		return err
	}
	if err := s.CaptureActive(cfg); err != nil {
		return fmt.Errorf("sync active profile: %w", err)
	}
	if err := profile.Clear(s.liveDir); err != nil {
		return fmt.Errorf("clear live dir: %w", err)
	}
	cfg.ActiveProfile = ""
	if err := s.store.Save(cfg); err != nil {
		return err
	}
	return s.app.Launch()
}
