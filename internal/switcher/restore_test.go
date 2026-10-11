package switcher

import (
	"testing"
	"time"
)

func TestRestorerReopensAfterUpdateQuit(t *testing.T) {
	r := NewRestorer()
	t0 := time.Date(2026, 10, 10, 7, 51, 40, 0, time.UTC)
	ids := []string{"work", "gmail"}
	both := map[string]bool{"work": true, "gmail": true}

	r.Observe(t0, ids, both, time.Time{})
	// Both windows vanish; the log shows an update quit a few seconds after the
	// last time they were seen.
	t1 := t0.Add(5 * time.Second)
	r.Observe(t1, ids, map[string]bool{}, t0.Add(3*time.Second))

	if got := r.Due(t1); len(got) != 0 {
		t.Fatalf("should wait for the updater, got %v", got)
	}
	got := r.Due(t1.Add(restoreDelay))
	if len(got) != 2 {
		t.Fatalf("want both windows due, got %v", got)
	}
	r.Done(got)
	if again := r.Due(t1.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("handed-off windows must not repeat, got %v", again)
	}
}

func TestRestorerLeavesClosedWindowsAlone(t *testing.T) {
	r := NewRestorer()
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	r.Observe(t0, []string{"work"}, map[string]bool{"work": true}, time.Time{})

	// The window disappears, but the only update quit in the log is old.
	t1 := t0.Add(5 * time.Second)
	r.Observe(t1, []string{"work"}, map[string]bool{}, t0.Add(-2*time.Hour))
	if got := r.Due(t1.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("a window closed by the user must stay closed, got %v", got)
	}
}

func TestRestorerIgnoresWindowsItNeverSaw(t *testing.T) {
	r := NewRestorer()
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	r.Observe(t0, []string{"work"}, map[string]bool{}, t0)
	if got := r.Due(t0.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
