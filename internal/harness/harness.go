// Package harness knows where each AI coding tool keeps its user-level files.
// Project-level files are out of scope: CD-Switcher only manages the global
// setup of the machine.
package harness

import (
	"os"
	"path/filepath"
)

// Home is swapped in tests.
var Home = func() string {
	h, _ := os.UserHomeDir()
	return h
}

// Names of the tools, as stored in CD-Switcher's config.
const (
	Claude   = "claude"   // Claude Code
	Desktop  = "desktop"  // Claude Desktop
	Codex    = "codex"    // OpenAI Codex CLI
	Cursor   = "cursor"   // Cursor
	OpenCode = "opencode" // OpenCode
	Gemini   = "gemini"   // Gemini CLI
	Qwen     = "qwen"     // Qwen Code
)

// Title is the display name of a tool.
func Title(name string) string {
	return map[string]string{
		Claude: "Claude Code", Desktop: "Claude Desktop", Codex: "Codex",
		Cursor: "Cursor", OpenCode: "OpenCode", Gemini: "Gemini CLI", Qwen: "Qwen Code",
	}[name]
}

// ClaudeDir is ~/.claude. CLAUDE_CONFIG_DIR is not honoured: a menu-bar app
// started from Finder never sees the user's shell environment.
func ClaudeDir() string { return filepath.Join(Home(), ".claude") }

// ClaudeSettings is Claude Code's user settings file.
func ClaudeSettings() string { return filepath.Join(ClaudeDir(), "settings.json") }

// ClaudeJSON is Claude Code's global state file (MCP servers, account), or
// the legacy ~/.claude/.config.json when that one exists and is not empty.
func ClaudeJSON() string {
	legacy := filepath.Join(ClaudeDir(), ".config.json")
	if fi, err := os.Stat(legacy); err == nil && fi.Size() > 0 {
		return legacy
	}
	return filepath.Join(Home(), ".claude.json")
}

// CodexHome is $CODEX_HOME or ~/.codex.
func CodexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(Home(), ".codex")
}

// xdgConfig is $XDG_CONFIG_HOME or ~/.config, which OpenCode uses on every OS.
func xdgConfig() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".config")
}

// CursorDir, GeminiDir, QwenDir and OpenCodeDir are each tool's user folder.
func CursorDir() string   { return filepath.Join(Home(), ".cursor") }
func GeminiDir() string   { return filepath.Join(Home(), ".gemini") }
func QwenDir() string     { return filepath.Join(Home(), ".qwen") }
func OpenCodeDir() string { return filepath.Join(xdgConfig(), "opencode") }

// OpenCodeConfig is opencode.json, or opencode.jsonc when only that exists.
func OpenCodeConfig() string {
	plain := filepath.Join(OpenCodeDir(), "opencode.json")
	if _, err := os.Stat(plain); err != nil {
		if c := filepath.Join(OpenCodeDir(), "opencode.jsonc"); exists(c) {
			return c
		}
	}
	return plain
}

// AgentsSkills is ~/.agents/skills, which Codex, Gemini CLI, Cursor,
// OpenCode and Qwen Code all read.
func AgentsSkills() string { return filepath.Join(Home(), ".agents", "skills") }

// Installed reports whether a tool's user folder exists. desktopDir is Claude
// Desktop's data directory ("" when it is not installed).
func Installed(name, desktopDir string) bool {
	switch name {
	case Claude:
		return exists(ClaudeDir())
	case Desktop:
		return desktopDir != "" && exists(desktopDir)
	case Codex:
		return exists(CodexHome())
	case Cursor:
		return exists(CursorDir())
	case OpenCode:
		return exists(OpenCodeDir())
	case Gemini:
		return exists(GeminiDir())
	case Qwen:
		return exists(QwenDir())
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
