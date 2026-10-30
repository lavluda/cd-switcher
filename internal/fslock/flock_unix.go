//go:build !windows

package fslock

import (
	"os"
	"syscall"
	"time"
)

// FileLock holds an exclusive flock on a file, the lock Rust's File::lock
// takes, which Codex uses for $CODEX_HOME/auth.lock.
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
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &FileLock{f: f}, nil
		}
		if err != syscall.EWOULDBLOCK || time.Since(start) >= wait {
			f.Close()
			if err == syscall.EWOULDBLOCK {
				return nil, ErrBusy
			}
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Unlock releases the lock.
func (l *FileLock) Unlock() { _ = l.f.Close() }
