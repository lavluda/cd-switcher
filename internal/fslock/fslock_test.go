package fslock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockWaitsThenTakesOverStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := Acquire(path, time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, time.Minute, 200*time.Millisecond); err != ErrBusy {
		t.Fatalf("second holder: err = %v, want ErrBusy", err)
	}
	first.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("release left the lock")
	}

	// A lock left by a dead holder is taken over once it is stale.
	os.Mkdir(path, 0o755)
	old := time.Now().Add(-2 * time.Minute)
	os.Chtimes(path, old, old)
	l, err := Acquire(path, time.Minute, 0)
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	l.Release()
}
