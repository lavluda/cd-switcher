// Package agents follows coding-agent sessions through their hooks, so the
// tray can show which ones are working, which wait for the user and which have
// finished.
//
// Claude Code and Codex share one hooks schema: CD-Switcher adds itself as a
// command hook for a few events in each. Every event runs this binary with
// Arg, which records the session's state in a small file under the state
// directory; the app reads those files. Nothing is sent anywhere.
package agents

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

// Arg is the subcommand the hooks run, followed by the tool name.
const Arg = "agent-hook"

// State is where a session stands.
type State string

const (
	Running State = "running" // working on a prompt
	Waiting State = "waiting" // needs the user: a permission or a question
	Done    State = "done"    // finished its turn
)

// Session is one agent session as its hooks last reported it.
type Session struct {
	Tool    string    `json:"tool"`
	ID      string    `json:"id"`
	Cwd     string    `json:"cwd,omitempty"`
	State   State     `json:"state"`
	Message string    `json:"message,omitempty"`
	App     string    `json:"app,omitempty"` // macOS bundle ID of the app the session runs in
	At      time.Time `json:"at"`
}

// Project is the session's folder name.
func (s Session) Project() string {
	if s.Cwd == "" {
		return harness.Title(s.Tool)
	}
	return filepath.Base(s.Cwd)
}

// maxAge drops sessions whose agent was closed without a SessionEnd hook.
const maxAge = 24 * time.Hour

func dir(stateDir string) string { return filepath.Join(stateDir, "agents") }

func path(stateDir, tool, id string) string {
	return filepath.Join(dir(stateDir), tool+"_"+safe(id)+".json")
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

type event struct {
	Name      string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Type      string `json:"notification_type"`
	Message   string `json:"message"`
	Last      string `json:"last_assistant_message"`
}

// RunHook records one hook event. It prints nothing and never fails: output
// from a PermissionRequest or Stop hook would be read as a decision.
func RunHook(stateDir, tool string, in io.Reader) {
	b, _ := io.ReadAll(io.LimitReader(in, 4<<20))
	var ev event
	if json.Unmarshal(b, &ev) != nil || ev.SessionID == "" {
		return
	}
	apply(stateDir, tool, ev, os.Getenv("__CFBundleIdentifier"), time.Now())
}

func apply(stateDir, tool string, ev event, app string, now time.Time) {
	p := path(stateDir, tool, ev.SessionID)
	var next State
	msg := ""
	switch ev.Name {
	case "SessionEnd":
		_ = os.Remove(p)
		return
	case "UserPromptSubmit", "PostToolUse":
		next = Running
	case "PermissionRequest":
		next, msg = Waiting, "Needs your permission"
	case "Notification":
		switch {
		case ev.Type == "permission_prompt", ev.Type == "elicitation_dialog",
			ev.Type == "" && strings.Contains(strings.ToLower(ev.Message), "permission"):
			next, msg = Waiting, ev.Message
		default:
			return // idle reminders and sign-in notices
		}
	case "Stop":
		next, msg = Done, ev.Last
	case "StopFailure":
		next, msg = Done, "Stopped on an error"
	default:
		return
	}
	s := Session{Tool: tool, ID: ev.SessionID}
	if old, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(old, &s)
		// PostToolUse fires on every tool call; skip the write when nothing changes.
		if s.State == next && next == Running {
			return
		}
	}
	s.State, s.Message, s.At = next, oneLine(msg), now
	if ev.Cwd != "" {
		s.Cwd = ev.Cwd
	}
	if app != "" {
		s.App = app
	}
	out, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = writeAtomic(p, out)
}

// oneLine shortens an agent's message to a menu-sized line.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 90 {
		return string(r[:89]) + "…"
	}
	return s
}

// writeAtomic uses a unique temp file: parallel tool calls run several hooks
// for the same session at once.
func writeAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return errors.Join(werr, cerr)
	}
	return os.Rename(f.Name(), p)
}

// List returns the live sessions, the ones needing the user first, then the
// newest. Sessions older than a day are deleted.
func List(stateDir string, now time.Time) []Session {
	entries, err := os.ReadDir(dir(stateDir))
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir(stateDir), e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) != nil || now.Sub(s.At) > maxAge {
			_ = os.Remove(p)
			continue
		}
		out = append(out, s)
	}
	rank := map[State]int{Waiting: 0, Running: 1, Done: 2}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].State] != rank[out[j].State] {
			return rank[out[i].State] < rank[out[j].State]
		}
		return out[i].At.After(out[j].At)
	})
	return out
}

// Clear forgets a session, as when the user has seen that it finished.
func Clear(stateDir, tool, id string) error {
	err := os.Remove(path(stateDir, tool, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
