package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"github.com/ncruces/zenity"

	"github.com/lavluda/cd-switcher/internal/agents"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// inboxPoll is how often the agent session files are read.
// shortcut: polling, not file events; fine for a few dozen small files, use fsnotify if it ever shows up in CPU.
const inboxPoll = 2 * time.Second

// inboxMax is how many sessions the tray menu lists.
const inboxMax = 8

// inboxInstalled reports whether any tool runs CD-Switcher's agent hooks.
func inboxInstalled() bool {
	for _, tool := range agents.Tools {
		if agents.Installed(tool) {
			return true
		}
	}
	return false
}

func (a *app) doSetInbox(on bool) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	codex := false
	for _, tool := range agents.Tools {
		if !on {
			if err := agents.Uninstall(tool); err != nil {
				return err
			}
			continue
		}
		if !harness.Installed(tool, "") {
			continue
		}
		if err := agents.Install(tool, exe); err != nil {
			return fmt.Errorf("%s: %w", harness.Title(tool), err)
		}
		codex = codex || tool == harness.Codex
	}
	if codex {
		_ = zenity.Notify("Agent inbox is on. In Codex, run /hooks once and trust CD-Switcher's hooks.")
	}
	return nil
}

// watchInbox follows agent sessions: it notifies when one starts waiting for
// the user or finishes, and keeps the tray list current.
func (a *app) watchInbox() {
	// A moved or updated app keeps its hooks pointing at the current binary.
	a.queueOp(func() error {
		exe, err := selfPath()
		if err != nil {
			return nil
		}
		for _, tool := range agents.Tools {
			if agents.Installed(tool) {
				if err := agents.Install(tool, exe); err != nil {
					logf("agent hooks %s: %v", tool, err)
				}
			}
		}
		return nil
	})
	seen := map[string]agents.Session{}
	first := true
	for range time.Tick(inboxPoll) {
		list := agents.List(a.store.Root(), time.Now())
		changed := len(list) != len(seen)
		next := make(map[string]agents.Session, len(list))
		for _, s := range list {
			key := s.Tool + "/" + s.ID
			next[key] = s
			old, had := seen[key]
			if had && old.State == s.State && old.Message == s.Message {
				continue
			}
			changed = true
			if !first && (s.State == agents.Waiting || s.State == agents.Done) && (!had || old.State != s.State) {
				_ = zenity.Notify(inboxNotice(s))
			}
		}
		seen, first = next, false
		if changed {
			a.mu.Lock()
			a.agentSessions = list
			a.mu.Unlock()
			fyne.Do(a.buildAndSetMenu)
		}
	}
}

func inboxNotice(s agents.Session) string {
	who := s.Project() + " (" + harness.Title(s.Tool) + ")"
	if s.State == agents.Waiting {
		if s.Message != "" {
			return who + " needs you: " + s.Message
		}
		return who + " needs you"
	}
	if s.Message != "" {
		return who + " finished: " + s.Message
	}
	return who + " finished"
}

// inboxMenuItems is the Agents block of the tray menu.
func (a *app) inboxMenuItems() []*fyne.MenuItem {
	a.mu.Lock()
	list := a.agentSessions
	a.mu.Unlock()
	if len(list) == 0 {
		return nil
	}
	head := fyne.NewMenuItem("Agents", nil)
	head.Disabled = true
	items := []*fyne.MenuItem{head}
	anyDone := false
	for i, s := range list {
		anyDone = anyDone || s.State == agents.Done
		if i >= inboxMax {
			continue
		}
		s := s
		items = append(items, fyne.NewMenuItem(inboxLabel(s), func() { a.openSession(s) }))
	}
	if anyDone {
		items = append(items, fyne.NewMenuItem("Clear finished", func() {
			for _, s := range list {
				if s.State == agents.Done {
					_ = agents.Clear(a.store.Root(), s.Tool, s.ID)
				}
			}
		}))
	}
	return items
}

func inboxLabel(s agents.Session) string {
	name := s.Project() + " · " + harness.Title(s.Tool)
	switch s.State {
	case agents.Waiting:
		return "✋ " + name + " — needs you"
	case agents.Done:
		// A clock time, not "5m ago": the menu is only rebuilt on changes.
		return "✓ " + name + " — done at " + s.At.Local().Format("15:04")
	}
	return "⏳ " + name + " — working"
}

// openSession brings the session's app forward and, for a finished session,
// takes it off the list.
// shortcut: macOS only and app-level (not the exact tab); hooks see no window ID.
func (a *app) openSession(s agents.Session) {
	if s.State == agents.Done {
		_ = agents.Clear(a.store.Root(), s.Tool, s.ID)
	}
	if runtime.GOOS == "darwin" && s.App != "" {
		if err := exec.Command("open", "-b", s.App).Run(); err != nil {
			logf("open %s: %v", s.App, err)
		}
	}
}
