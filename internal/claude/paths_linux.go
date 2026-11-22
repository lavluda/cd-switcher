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
// rather than the "claude" CLI (Claude Code), which shares the process name.
// Treating the CLI as Desktop is dangerous: Quit() would SIGTERM/SIGKILL every
// running Claude Code session. The native Claude Code installer runs from
// ~/.local/share/claude/versions/<version>, a path with no "claude-code" in
// it, so excluding by "claude-code" alone is not enough. When the executable
// path is known, require it to be Claude Desktop's own: the claude-desktop
// binary, or a file inside a claude-desktop or capitalised "Claude" install
// (an AppImage mounts as /tmp/.mount_Claude*).
func isClaudeDesktopProcess(name, exe string) bool {
	if !strings.HasPrefix(strings.ToLower(name), "claude") {
		return false
	}
	if exe == "" {
		// Unreadable exe (another user's process): fall back to the name.
		return true
	}
	lower := strings.ToLower(exe)
	if strings.Contains(lower, "claude-code") {
		return false
	}
	if strings.ToLower(filepath.Base(exe)) == "claude-desktop" {
		return true
	}
	return strings.Contains(lower, "claude-desktop") || strings.Contains(exe, "Claude")
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
	path, err := desktopExecutable()
	if err != nil {
		return err
	}
	return exec.Command(path).Start()
}

func launchAt(dir string) error {
	path, err := desktopExecutable()
	if err != nil {
		return err
	}
	return exec.Command(path, "--user-data-dir="+dir).Start()
}

func desktopExecutable() (string, error) {
	if path, err := exec.LookPath("claude-desktop"); err == nil {
		return path, nil
	}
	return exec.LookPath("claude")
}

// mainLogPath is where Claude Desktop writes its main-process log.
func mainLogPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs", "main.log"), nil
}
