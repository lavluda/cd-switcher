package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// DataDirFromArgs returns the --user-data-dir value from a process argv, if
// the process was started as a side-by-side instance.
func DataDirFromArgs(args []string) (string, bool) {
	for i, a := range args {
		if a == "--user-data-dir" && i+1 < len(args) {
			return filepath.Clean(args[i+1]), true
		}
		const prefix = "--user-data-dir="
		if strings.HasPrefix(a, prefix) {
			return filepath.Clean(strings.TrimPrefix(a, prefix)), true
		}
	}
	return "", false
}

// LaunchAt starts a separate Claude Desktop process whose data lives in dir.
// It returns an error if that process is not still running when the launch
// window closes — Desktop may refuse a second instance.
func LaunchAt(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return err
	}
	deadline := time.Now().Add(launchTimeout)
	var lastErr error
	for {
		if ok, err := DataDirRunning(abs); err == nil && ok {
			return nil
		}
		if err := launchAt(abs); err != nil {
			lastErr = err
		}
		time.Sleep(1500 * time.Millisecond)
		if ok, err := DataDirRunning(abs); err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("claude desktop did not stay open for %s: %w", abs, lastErr)
			}
			return fmt.Errorf("claude desktop did not stay open with a separate data directory (%s)", abs)
		}
	}
}

// QuitAt asks the Claude process whose --user-data-dir is dir to exit.
func QuitAt(dir string, timeout time.Duration) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	return quitMatching(timeout, func(args []string) bool {
		got, ok := DataDirFromArgs(args)
		return ok && got == abs
	})
}

// QuitDefault asks the Claude process that uses the normal data directory
// (no --user-data-dir) to exit, leaving side-by-side windows alone.
func QuitDefault(timeout time.Duration) error {
	return quitMatching(timeout, func(args []string) bool {
		_, ok := DataDirFromArgs(args)
		return !ok
	})
}

// DataDirRunning reports whether a Claude process was started with dir as
// its --user-data-dir.
func DataDirRunning(dir string) (bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	abs = filepath.Clean(abs)
	return anyDesktop(func(args []string) bool {
		got, ok := DataDirFromArgs(args)
		return ok && got == abs
	})
}

// RunningDataDirs returns the --user-data-dir of every side-by-side Claude
// process currently alive.
func RunningDataDirs() ([]string, error) {
	var dirs []string
	err := walkDesktop(func(args []string) {
		if dir, ok := DataDirFromArgs(args); ok {
			dirs = append(dirs, dir)
		}
	})
	return dirs, err
}

func defaultRunning() (bool, error) {
	return anyDesktop(func(args []string) bool {
		_, ok := DataDirFromArgs(args)
		return !ok
	})
}

func anyDesktop(pred func(args []string) bool) (bool, error) {
	found := false
	err := walkDesktop(func(args []string) {
		if pred(args) {
			found = true
		}
	})
	return found, err
}

func walkDesktop(fn func(args []string)) error {
	procs, err := process.Processes()
	if err != nil {
		return err
	}
	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue
		}
		exe, _ := p.Exe()
		if !isClaudeDesktopProcess(name, exe) {
			continue
		}
		args, _ := p.CmdlineSlice()
		fn(args)
	}
	return nil
}

func quitMatching(timeout time.Duration, match func(args []string) bool) error {
	if err := signalMatching(match, false); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	escalated := false
	for {
		still, err := anyDesktop(match)
		if err != nil {
			return err
		}
		if !still {
			return nil
		}
		if time.Now().After(deadline) {
			if !escalated {
				escalated = true
				_ = signalMatching(match, true)
				deadline = time.Now().Add(3 * time.Second)
				continue
			}
			return errors.New("claude desktop did not exit in time")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func signalMatching(match func(args []string) bool, force bool) error {
	procs, err := process.Processes()
	if err != nil {
		return err
	}
	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue
		}
		exe, _ := p.Exe()
		if !isClaudeDesktopProcess(name, exe) {
			continue
		}
		args, _ := p.CmdlineSlice()
		if !match(args) {
			continue
		}
		if force {
			_ = p.Kill()
		} else {
			_ = p.Terminate()
		}
	}
	return nil
}
