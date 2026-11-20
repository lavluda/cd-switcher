// Package fslock takes the directory locks Claude Code uses (the
// proper-lockfile protocol), so CD-Switcher's writes never interleave with its.
package fslock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrBusy means Claude Code held one of its locks for longer than we wait.
var ErrBusy = errors.New("Claude Code is using its sign-in right now (refreshing or signing in); try switching again in a moment")

// Lock is one of Claude Code's own locks. It uses the proper-lockfile
// protocol: the lock is a directory, mkdir takes it, removing it releases it,
// and one whose mtime is older than its stale age belongs to a dead holder.
// We hold locks for far less than their stale age, so we never refresh the
// mtime; on release we only remove the directory if it is still ours.
type Lock struct {
	path  string
	mtime time.Time
}

// Acquire takes the lock at path, waiting up to wait. A lock older than stale
// is treated as abandoned and taken over.
func Acquire(path string, stale, wait time.Duration) (*Lock, error) {
	start := time.Now()
	madeParent := false
	for {
		err := os.Mkdir(path, 0o755)
		switch {
		case err == nil:
			return &Lock{path: path, mtime: mtimeOf(path)}, nil
		case errors.Is(err, fs.ErrExist):
			if seen := mtimeOf(path); !seen.IsZero() && time.Since(seen) > stale {
				// Check again right before removing: another waiter may have
				// replaced the stale lock with its own in between.
				if mtimeOf(path).Equal(seen) && os.Remove(path) == nil {
					continue
				}
			}
		case errors.Is(err, fs.ErrNotExist) && !madeParent:
			madeParent = true
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, err
			}
			continue
		default:
			return nil, err
		}
		if time.Since(start) >= wait {
			return nil, ErrBusy
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Release removes the lock unless someone took it over as stale.
func (l *Lock) Release() {
	if m := mtimeOf(l.path); !m.IsZero() && m.Equal(l.mtime) {
		_ = os.Remove(l.path)
	}
}

func mtimeOf(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
