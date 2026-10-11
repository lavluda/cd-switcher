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
// Desktop (the app we manage) rather than some other program whose name merely
// starts with "Claude". Two lookalikes matter:
//
//   - The bundled "claude" CLI (Claude Code): shares the executable name
//     "claude" but lives under a "claude-code" directory, so its path gives it
//     away. Treating it as Desktop made Quit() wait forever for a CLI session.
//   - Unrelated apps like the third-party "Claude Usage.app" menu-bar tool:
//     name "Claude Usage" has the "claude" prefix but is not our app. Counting
//     it made Quit() block until timeout — forceQuit's `pkill -f Claude.app`
//     can't match "Claude Usage.app", so it never dies — failing every switch.
//
// The reliable signal is the executable path: real Desktop processes (main
// "Claude" and the "Claude Helper*" family) all live inside the "Claude.app"
// bundle. When the path is unreadable we fall back to the exact process names
// Desktop uses.
func isClaudeDesktopProcess(name, exe string) bool {
	if !strings.HasPrefix(strings.ToLower(name), "claude") {
		return false
	}
	if strings.Contains(strings.ToLower(exe), "claude-code") {
		return false
	}
	if exe != "" {
		return strings.Contains(exe, "/Claude.app/")
	}
	// No path: accept only the names Desktop actually uses, so lookalikes such
	// as "Claude Usage" don't slip through on the name prefix alone.
	return name == "Claude" || strings.HasPrefix(name, "Claude Helper")
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

// launchAt opens a new instance bound to dir. -n is required so Launch
// Services does not hand the arguments to the window that is already open.
func launchAt(dir string) error {
	return exec.Command("open", "-n", "-a", "Claude", "--args", "--user-data-dir="+dir).Start()
}

// mainLogPath is where Claude Desktop writes its main-process log.
func mainLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "Claude", "main.log"), nil
}
