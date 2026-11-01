package agents

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// events are the hooks each tool gets. Codex has no Notification event, and
// Claude Code's PermissionRequest and permission Notification both mean
// waiting, so either one arriving first is enough.
var events = map[string][]string{
	harness.Claude: {"UserPromptSubmit", "PostToolUse", "PermissionRequest", "Notification", "Stop", "StopFailure", "SessionEnd"},
	harness.Codex:  {"UserPromptSubmit", "PostToolUse", "PermissionRequest", "Stop", "SessionEnd"},
}

// Tools are the tools whose sessions can be followed.
var Tools = []string{harness.Claude, harness.Codex}

// hooksFile is where a tool keeps its user-level hooks.
func hooksFile(tool string) string {
	if tool == harness.Codex {
		return filepath.Join(harness.CodexHome(), "hooks.json")
	}
	return harness.ClaudeSettings()
}

// Command is the hook command line that runs exe for tool.
func Command(exe, tool string) string {
	if runtime.GOOS == "windows" {
		return `"` + exe + `" ` + Arg + " " + tool
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' " + Arg + " " + tool
}

func isOurs(command string) bool {
	return strings.Contains(command, " "+Arg+" ") && strings.Contains(strings.ToLower(command), "cd-switcher")
}

type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// Installed reports whether tool's hooks run CD-Switcher.
func Installed(tool string) bool {
	doc, err := cfgfile.ReadJSON(hooksFile(tool))
	if err != nil {
		return false
	}
	hooks, err := doc.Child("hooks")
	if err != nil {
		return false
	}
	var groups []json.RawMessage
	if hooks.Get("Stop", &groups) != nil {
		return false
	}
	for _, g := range groups {
		if _, ok := ours(g); ok {
			return true
		}
	}
	return false
}

// ours returns the command of a hook group CD-Switcher added.
func ours(group json.RawMessage) (string, bool) {
	var g struct {
		Hooks []handler `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return "", false
	}
	for _, h := range g.Hooks {
		if isOurs(h.Command) {
			return h.Command, true
		}
	}
	return "", false
}

// Install adds exe as a hook for tool's events, replacing an older copy (a
// moved app), and leaves every other hook alone.
func Install(tool, exe string) error {
	return edit(tool, Command(exe, tool))
}

// Uninstall removes CD-Switcher's hooks from tool.
func Uninstall(tool string) error { return edit(tool, "") }

func edit(tool, command string) error {
	return cfgfile.UpdateJSON(hooksFile(tool), 0, false, func(doc *cfgfile.Object) (bool, error) {
		hooks, err := doc.Child("hooks")
		if err != nil {
			return false, err
		}
		changed := false
		for _, ev := range events[tool] {
			var groups []json.RawMessage
			if err := hooks.Get(ev, &groups); err != nil {
				return false, err
			}
			kept, placed := groups[:0:0], false
			for _, g := range groups {
				if c, ok := ours(g); ok {
					if c != command || placed {
						changed = true
						continue
					}
					placed = true
				}
				kept = append(kept, g)
			}
			if command != "" && !placed {
				g, _ := json.Marshal(map[string]any{"hooks": []handler{{Type: "command", Command: command, Timeout: 5}}})
				kept = append(kept, g)
				changed = true
			}
			if len(kept) == 0 {
				hooks.Delete(ev)
			} else if err := hooks.Set(ev, kept); err != nil {
				return false, err
			}
		}
		if !changed {
			return false, nil
		}
		if hooks.Len() == 0 {
			doc.Delete("hooks")
			return true, nil
		}
		return true, doc.Set("hooks", hooks)
	})
}
