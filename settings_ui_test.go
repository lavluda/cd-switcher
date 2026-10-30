package main

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"github.com/lavluda/cd-switcher/internal/codex"
	"github.com/lavluda/cd-switcher/internal/copilot"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/instructions"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/provider"
)

// Builds every Settings tab, the extra tray sections and each form against a
// fake home folder, so a panic in any screen fails here instead of in the app.
func TestSettingsTabsAndFormsBuild(t *testing.T) {
	h := t.TempDir()
	oldHome := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = oldHome })
	t.Setenv("CODEX_HOME", "")

	test.NewApp()
	store, err := profile.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.store = store
	a.ops = make(chan func() error, 100)
	a.cfg = &profile.Config{
		Profiles:           []profile.Profile{{ID: "p1", Label: "Work"}, {ID: "p2", Label: "Home", NoAutoSwitch: true}},
		ActiveProfile:      "p1",
		CodexAccounts:      []profile.CodexAccount{{ID: "c1", Label: "me@x", Email: "me@x", Plan: "plus"}},
		ActiveCodex:        "c1",
		Providers:          []provider.Provider{{ID: "claude-or", Name: "OpenRouter", Harness: harness.Claude, BaseURL: "https://openrouter.ai/api"}, {ID: "codex-l", Name: "Local", Harness: harness.Codex, BaseURL: "http://localhost:11434/v1"}},
		ActiveProvider:     map[string]string{harness.Claude: "claude-or"},
		MCPServers:         []mcp.Server{{Name: "fs", Command: "npx", Args: []string{"-y", "fs"}, Enabled: map[string]bool{harness.Claude: true}}, {Name: "remote", URL: "https://r", Transport: "http"}},
		Instructions:       []instructions.Entry{{ID: "a", Name: "Mine", Body: "be brief\nmore"}},
		ActiveInstructions: "a",
		InstructionTools:   map[string]bool{harness.Claude: true},
		AutoSwitchStrategy: profile.StrategyConsumeFirst,
	}
	a.codexCache = map[string]codexEntry{"c1": {quota: codex.Quota{Plan: "plus", Session: codex.Window{Percent: 30, Seconds: 18000, ResetsAt: time.Now().Add(time.Hour)}, Weekly: codex.Window{Percent: 60, Seconds: 604800}}, fetchedAt: time.Now()}}
	a.copilot = copilotEntry{quota: copilot.Quota{Login: "me", Plan: "pro", Percent: 24, Remaining: 228, Total: 300}, fetchedAt: time.Now()}

	w := test.NewWindow(nil)
	a.settingsWin = w
	content := a.settingsContent()
	tabs := content.(*container.AppTabs)
	w.SetContent(content)
	w.Resize(fyne.NewSize(900, 700))
	for i, item := range tabs.Items {
		tabs.SelectIndex(i)
		if item.Content.MinSize().Height <= 0 {
			t.Fatalf("tab %q rendered empty", item.Text)
		}
	}
	if n := len(a.codexMenuItems()); n < 4 {
		t.Fatalf("codex menu items = %d", n)
	}
	if items := a.toolMenuItems(); len(items) == 0 || items[0].ChildMenu == nil {
		t.Fatal("provider submenu missing")
	}
	a.providerForm(harness.Claude, provider.Provider{Harness: harness.Claude})
	a.providerForm(harness.Codex, a.cfg.Providers[1])
	a.mcpForm(mcp.Server{}, "")
	a.instructionsForm(instructions.Entry{})
	a.mcpPasteForm()
}
