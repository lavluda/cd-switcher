package switcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lavluda/cd-switcher/internal/profile"
)

// fakeApp records quit/launch calls and simulates Claude being stopped/started.
type fakeApp struct {
	quits    int
	launches int
	running  bool
}

func (f *fakeApp) Quit(time.Duration) error {
	f.quits++
	f.running = false
	return nil
}

func (f *fakeApp) Launch() error {
	f.launches++
	f.running = true
	return nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sessionKey reads the fake "Cookies" file standing in for the account session.
func sessionKey(t *testing.T, liveDir string) string {
	return read(t, filepath.Join(liveDir, "Cookies"))
}

// TestFullSwitchCycle exercises the real orchestration end to end against temp
// dirs and a fake app controller: capture account A, capture account B, then
// switch A<->B and confirm the live session swaps, the app is bounced each time,
// and the prior account's state is synced back (not lost).
func TestFullSwitchCycle(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	store, err := profile.NewStoreAt(filepath.Join(tmp, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	app := &fakeApp{running: true}
	sw := newWith(store, live, app)
	cfg := &profile.Config{}

	// --- Account A is currently signed in. Capture it. ---
	write(t, filepath.Join(live, "Cookies"), "session=A")
	write(t, filepath.Join(live, "Cache", "junk"), "cacheA") // must never travel
	if err := sw.CaptureAs(cfg, profile.Profile{ID: "a", Label: "A"}); err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveProfile != "a" {
		t.Fatalf("active = %q, want a", cfg.ActiveProfile)
	}

	// --- Simulate logging into account B, then capture it. ---
	write(t, filepath.Join(live, "Cookies"), "session=B")
	if err := sw.CaptureAs(cfg, profile.Profile{ID: "b", Label: "B"}); err != nil {
		t.Fatal(err)
	}

	// --- Switch to A: live session must become A again. ---
	if err := sw.SwitchTo(cfg, "a", true); err != nil {
		t.Fatal(err)
	}
	if got := sessionKey(t, live); got != "session=A" {
		t.Fatalf("after switch to A, session = %q", got)
	}
	if cfg.ActiveProfile != "a" {
		t.Fatalf("active = %q, want a", cfg.ActiveProfile)
	}
	if !app.running {
		t.Fatal("Claude should have been relaunched")
	}

	// --- While on A, create new local state, then switch to B. ---
	write(t, filepath.Join(live, "draft.txt"), "A-draft")
	if err := sw.SwitchTo(cfg, "b", true); err != nil {
		t.Fatal(err)
	}
	if got := sessionKey(t, live); got != "session=B" {
		t.Fatalf("after switch to B, session = %q", got)
	}
	// B never had a draft; A's draft must not leak across.
	if _, err := os.Stat(filepath.Join(live, "draft.txt")); !os.IsNotExist(err) {
		t.Fatal("A's draft leaked into B's live dir")
	}

	// --- Switch back to A: the synced-back draft must reappear. ---
	if err := sw.SwitchTo(cfg, "a", true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(live, "draft.txt")); got != "A-draft" {
		t.Fatalf("A's draft not preserved across round-trip, got %q", got)
	}

	// Each real switch should have quit + relaunched the app.
	if app.quits < 3 || app.launches < 3 {
		t.Fatalf("quits=%d launches=%d, want >=3 each", app.quits, app.launches)
	}

	// Cache must never have been copied into any snapshot.
	if _, err := os.Stat(filepath.Join(tmp, "cfg", "profiles", "a", "Cache")); !os.IsNotExist(err) {
		t.Fatal("cache should not be captured into a profile snapshot")
	}

	// No leftover backup dir after successful switches.
	if _, err := os.Stat(live + ".cdswitch-bak"); !os.IsNotExist(err) {
		t.Fatal("rollback backup dir should be cleaned up")
	}
}
