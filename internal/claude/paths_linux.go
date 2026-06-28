//go:build linux

package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"
)

// dataDir resolves $XDG_CONFIG_HOME/Claude (default ~/.config/Claude) for the
// community Linux builds of Claude Desktop.
func dataDir() (string, error) {
	if cfg := os.Getenv("XDG_CONFIG_HOME"); cfg != "" {
		return filepath.Join(cfg, "Claude"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "Claude"), nil
}

// isClaudeDesktopProcess reports whether a process belongs to Claude Desktop
// rather than the bundled "claude" CLI (Claude Code), which shares the same
// executable name. Without excluding the CLI, a running Claude Code session
// would make Quit() wait forever and abort every switch. Claude Code lives
// under a "claude-code" directory, so its executable path identifies it.
func isClaudeDesktopProcess(name, exe string) bool {
	if !strings.HasPrefix(strings.ToLower(name), "claude") {
		return false
	}
	if strings.Contains(strings.ToLower(exe), "claude-code") {
		return false
	}
	return true
}

// requestQuit sends SIGTERM for a graceful shutdown.
func requestQuit() error {
	return signalClaudeDesktop(syscall.SIGTERM)
}

// forceQuit sends SIGKILL to any survivors.
func forceQuit() error {
	return signalClaudeDesktop(syscall.SIGKILL)
}

// signalClaudeDesktop sends sig to every Claude Desktop process. It deliberately
// reuses isClaudeDesktopProcess (which excludes the bundled "claude" CLI / Claude
// Code by its executable path) rather than a broad `pkill -f claude`, so quitting
// Desktop never tears down an unrelated Claude Code session sharing the name.
func signalClaudeDesktop(sig syscall.Signal) error {
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
		if isClaudeDesktopProcess(name, exe) {
			_ = syscall.Kill(int(p.Pid), sig)
		}
	}
	return nil
}

// launch starts whichever "claude" launcher is on PATH.
func launch() error {
	if path, err := exec.LookPath("claude-desktop"); err == nil {
		return exec.Command(path).Start()
	}
	return exec.Command("claude").Start()
}
