//go:build darwin

package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dataDir resolves ~/Library/Application Support/Claude.
func dataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Claude"), nil
}

// isClaudeDesktopProcess reports whether a running process belongs to Claude
// Desktop (the app we manage) rather than the bundled "claude" CLI (Claude
// Code). Both run under an executable named "claude", so matching on name alone
// would mistake a running CLI session for Desktop and make Quit() wait forever
// for it to exit — aborting every switch. Claude Code lives under a
// "claude-code" directory inside the data dir, so its executable path gives it
// away. The main bundle process is "Claude" and helpers are "Claude Helper",
// "Claude Helper (Renderer)", etc.
func isClaudeDesktopProcess(name, exe string) bool {
	if !strings.HasPrefix(strings.ToLower(name), "claude") {
		return false
	}
	if strings.Contains(strings.ToLower(exe), "claude-code") {
		return false
	}
	return true
}

// requestQuit asks the app to quit via AppleScript (clean shutdown).
func requestQuit() error {
	return exec.Command("osascript", "-e", `tell application "Claude" to quit`).Run()
}

// forceQuit kills any lingering Claude processes by name.
func forceQuit() error {
	return exec.Command("pkill", "-f", "Claude.app").Run()
}

// launch opens the app bundle by name.
func launch() error {
	return exec.Command("open", "-a", "Claude").Start()
}
