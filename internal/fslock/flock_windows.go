//go:build windows

package fslock

import "time"

// FileLock is a no-op on Windows.
// shortcut: no LockFileEx here, so a Codex token refresh can race an account
// switch on Windows; add LockFileEx if Windows users report a lost login.
type FileLock struct{}

func LockFile(path string, wait time.Duration) (*FileLock, error) { return &FileLock{}, nil }

func (l *FileLock) Unlock() {}
