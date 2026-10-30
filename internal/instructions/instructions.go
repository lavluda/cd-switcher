// Package instructions keeps one set of global instructions in step across
// the AI tools' user-level instruction files.
package instructions

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Target is one tool's global instructions file.
type Target struct {
	Tool string
	Path string
}

// Targets lists the files of the tools installed on this machine.
func Targets() []Target {
	all := []Target{
		{harness.Claude, filepath.Join(harness.ClaudeDir(), "CLAUDE.md")},
		{harness.Codex, filepath.Join(harness.CodexHome(), "AGENTS.md")},
		{harness.Gemini, filepath.Join(harness.GeminiDir(), "GEMINI.md")},
		{harness.OpenCode, filepath.Join(harness.OpenCodeDir(), "AGENTS.md")},
		{harness.Qwen, filepath.Join(harness.QwenDir(), "QWEN.md")},
	}
	var out []Target
	for _, t := range all {
		if harness.Installed(t.Tool, "") {
			out = append(out, t)
		}
	}
	return out
}

// Entry is one saved set of instructions.
type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Body string `json:"body"`
}

// Existing reads each tool's current file and returns one entry per distinct,
// non-empty text, named after the tools that have it.
func Existing() []Entry {
	var out []Entry
	index := map[string]int{}
	for _, t := range Targets() {
		b, err := os.ReadFile(t.Path)
		body := strings.TrimSpace(string(b))
		if err != nil || body == "" {
			continue
		}
		if i, ok := index[body]; ok {
			out[i].Name += ", " + harness.Title(t.Tool)
			continue
		}
		index[body] = len(out)
		out = append(out, Entry{ID: "from-" + t.Tool, Name: "From " + harness.Title(t.Tool), Body: string(b)})
	}
	return out
}

// Apply writes body to the files of the tools switched on in tools. The first
// write to each file is backed up by cfgfile.
func Apply(body string, tools map[string]bool) []error {
	var errs []error
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	for _, t := range Targets() {
		if !tools[t.Tool] {
			continue
		}
		if cur, err := os.ReadFile(t.Path); err == nil && string(cur) == body {
			continue
		}
		if err := cfgfile.WriteFile(t.Path, []byte(body), false); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// InSync reports, per installed tool, whether its file holds body.
func InSync(body string) map[string]bool {
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	out := map[string]bool{}
	for _, t := range Targets() {
		cur, err := os.ReadFile(t.Path)
		out[t.Tool] = err == nil && string(cur) == body
	}
	return out
}
