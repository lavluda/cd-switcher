//go:build windows

package fslock

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// FileLock holds an exclusive LockFileEx lock on a file, the lock Rust's
// File::lock takes on Windows, which Codex uses for $CODEX_HOME/auth.lock.
type FileLock struct{ f *os.File }

// LockFile takes an exclusive lock on path, creating the file, and waits up
// to wait for another holder to let go.
func LockFile(path string, wait time.Duration) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	for {
		ol := new(windows.Overlapped)
		err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
		if err == nil {
			return &FileLock{f: f}, nil
		}
		busy := errors.Is(err, windows.ERROR_LOCK_VIOLATION)
		if !busy || time.Since(start) >= wait {
			f.Close()
			if busy {
				return nil, ErrBusy
			}
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Unlock releases the lock; closing the handle drops it.
func (l *FileLock) Unlock() { _ = l.f.Close() }
