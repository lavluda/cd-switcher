// Package claude knows where Claude Desktop stores its data and how to
// launch / quit the app on each supported operating system. The OS-specific
// pieces live in the build-tagged paths_*.go files; this file holds the
// cross-platform glue.
package claude

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// ErrNotInstalled is returned when the Claude Desktop data directory cannot be
// located, which usually means Desktop has never been run on this machine.
var ErrNotInstalled = errors.New("claude desktop data directory not found")

// processNames lists the executable names Claude Desktop may run under,
// matched case-insensitively against the running process list.
var processNames = []string{"claude"}

// DataDir returns the absolute path to Claude Desktop's user-data directory,
// or ErrNotInstalled if its parent does not exist. The per-OS location is
// provided by dataDir() in the build-tagged files.
func DataDir() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", ErrNotInstalled
	}
	return dir, nil
}

// IsRunning reports whether a Claude Desktop process is currently alive.
func IsRunning() (bool, error) {
	p, err := findProcess()
	if err != nil {
		return false, err
	}
	return p != nil, nil
}

// findProcess returns the first matching Claude process, or nil if none.
func findProcess() (*process.Process, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}
	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue
		}
		// Exe() is best-effort: it lets the matcher tell Claude Desktop apart
		// from the bundled "claude" CLI (Claude Code), which shares the same
		// executable name. An empty path just means the matcher falls back to
		// the name alone.
		exe, _ := p.Exe()
		if isClaudeDesktopProcess(name, exe) {
			return p, nil
		}
	}
	return nil, nil
}

// Quit asks Claude Desktop to exit and waits up to timeout for every Claude
// process to disappear. It returns nil if the app is (or becomes) fully gone.
// If the app is still running after timeout, it returns an error so callers can
// abort before touching files. The graceful request is delegated to the
// OS-specific requestQuit(); if that does not take effect in time we escalate
// to forceQuit() once before the final wait.
func Quit(timeout time.Duration) error {
	running, err := IsRunning()
	if err != nil {
		return err
	}
	if !running {
		return nil
	}

	if err := requestQuit(); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	escalated := false
	for {
		running, err := IsRunning()
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			if !escalated {
				// One escalation pass, then a short grace window.
				escalated = true
				_ = forceQuit()
				deadline = time.Now().Add(3 * time.Second)
				continue
			}
			return errors.New("claude desktop did not exit in time")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// launchTimeout bounds how long Launch keeps trying to bring Claude up.
const launchTimeout = 12 * time.Second

// Launch starts Claude Desktop and verifies it actually comes up.
//
// A naive single launch() is unreliable right after Quit on macOS: the process
// is gone but LaunchServices still briefly considers the app "running", so an
// `open -a Claude` lands as an activate-no-op rather than a cold launch and the
// window never appears — a silent failure. To be robust we re-issue launch()
// until a Claude process is observed (or we time out), polling in between.
func Launch() error {
	deadline := time.Now().Add(launchTimeout)
	var lastErr error
	for attempt := 0; ; attempt++ {
		// Already up (either a prior attempt took, or it was never down)?
		if running, err := IsRunning(); err == nil && running {
			return nil
		}
		if err := launch(); err != nil {
			lastErr = err
		}
		// Give the launch a moment to register before re-checking / retrying.
		time.Sleep(1500 * time.Millisecond)
		if running, err := IsRunning(); err == nil && running {
			return nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("claude desktop did not come up: %w", lastErr)
			}
			return errors.New("claude desktop did not come up after launch")
		}
	}
}
