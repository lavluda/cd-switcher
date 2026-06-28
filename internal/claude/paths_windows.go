//go:build windows

package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dataDir resolves %APPDATA%\Claude.
func dataDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "Claude"), nil
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

// requestQuit asks Claude to close gracefully.
func requestQuit() error {
	return exec.Command("taskkill", "/IM", "claude.exe").Run()
}

// forceQuit force-terminates Claude and its children.
func forceQuit() error {
	return exec.Command("taskkill", "/IM", "claude.exe", "/T", "/F").Run()
}

// launch starts Claude Desktop. The installer drops a versioned exe under
// %LOCALAPPDATA%\AnthropicClaude\app-<ver>\claude.exe behind a stub
// %LOCALAPPDATA%\AnthropicClaude\claude.exe; we prefer the stub and fall back
// to the newest app-* directory.
func launch() error {
	exe := findExecutable()
	if exe == "" {
		// Last resort: let the shell resolve a registered "claude" command.
		return exec.Command("cmd", "/C", "start", "", "claude.exe").Start()
	}
	return exec.Command(exe).Start()
}

func findExecutable() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	root := filepath.Join(local, "AnthropicClaude")

	stub := filepath.Join(root, "claude.exe")
	if _, err := os.Stat(stub); err == nil {
		return stub
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var newest string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "app-") {
			continue
		}
		candidate := filepath.Join(root, e.Name(), "claude.exe")
		if _, err := os.Stat(candidate); err == nil {
			// Lexical max of app-<ver> is a good-enough "newest".
			if e.Name() > filepath.Base(filepath.Dir(newest)) {
				newest = candidate
			}
		}
	}
	return newest
}
