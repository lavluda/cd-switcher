package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestHookStates(t *testing.T) {
	state := t.TempDir()
	now := time.Now()
	run := func(json string) {
		t.Helper()
		RunHook(state, harness.Claude, strings.NewReader(json))
	}
	run(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","cwd":"/src/app"}`)
	run(`{"hook_event_name":"UserPromptSubmit","session_id":"s2","cwd":"/src/lib"}`)
	run(`{"hook_event_name":"Notification","session_id":"s2","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`)
	run(`{"hook_event_name":"Notification","session_id":"s1","notification_type":"idle_prompt","message":"waiting"}`)

	got := List(state, now)
	if len(got) != 2 || got[0].ID != "s2" || got[0].State != Waiting || got[1].State != Running || got[1].Project() != "app" {
		t.Fatalf("waiting first, idle ignored: %+v", got)
	}

	run(`{"hook_event_name":"PostToolUse","session_id":"s2"}`)
	run(`{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"All tests pass.\n\nDone."}`)
	got = List(state, now)
	if got[0].ID != "s2" || got[0].State != Running || got[0].Cwd != "/src/lib" {
		t.Fatalf("tool use resumes and keeps cwd: %+v", got[0])
	}
	if got[1].State != Done || got[1].Message != "All tests pass. Done." {
		t.Fatalf("stop: %+v", got[1])
	}

	run(`{"hook_event_name":"SessionEnd","session_id":"s2"}`)
	if got = List(state, now); len(got) != 1 {
		t.Fatalf("session end removes: %+v", got)
	}
	if got = List(state, now.Add(25*time.Hour)); len(got) != 0 {
		t.Fatalf("stale sessions expire: %+v", got)
	}
}

func TestInstallKeepsOtherHooks(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", filepath.Join(h, ".codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	settings := harness.ClaudeSettings()
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o600)
	os.MkdirAll(filepath.Join(h, ".cursor"), 0o700)
	os.WriteFile(filepath.Join(h, ".cursor", "hooks.json"), []byte(`{"hooks":{"stop":[{"command":"./audit.sh"}]}}`), 0o600)

	exe := "/Applications/CD-Switcher.app/Contents/MacOS/cd-switcher"
	for _, tool := range Tools {
		if err := Install(tool, exe); err != nil {
			t.Fatal(err)
		}
		if err := Install(tool, exe); err != nil { // second install is a no-op
			t.Fatal(err)
		}
		if !Installed(tool) {
			t.Fatalf("%s not installed", tool)
		}
	}
	b, _ := os.ReadFile(settings)
	if strings.Count(string(b), Arg) != len(events[harness.Claude]) || !strings.Contains(string(b), "say done") {
		t.Fatalf("one hook per event, user hook kept:\n%s", b)
	}
	codex, _ := os.ReadFile(filepath.Join(h, ".codex", "hooks.json"))
	if strings.Contains(string(codex), "Notification") || !strings.Contains(string(codex), "PermissionRequest") {
		t.Fatalf("codex events:\n%s", codex)
	}
	cursor, _ := os.ReadFile(filepath.Join(h, ".cursor", "hooks.json"))
	if !strings.Contains(string(cursor), `"version": 1`) || strings.Contains(string(cursor), `"type"`) ||
		!strings.Contains(string(cursor), "./audit.sh") || strings.Count(string(cursor), Arg) != len(events[harness.Cursor]) {
		t.Fatalf("cursor hooks:\n%s", cursor)
	}

	for _, tool := range Tools {
		if err := Uninstall(tool); err != nil {
			t.Fatal(err)
		}
		if Installed(tool) {
			t.Fatalf("%s still installed", tool)
		}
	}
	b, _ = os.ReadFile(settings)
	if strings.Contains(string(b), Arg) || !strings.Contains(string(b), "say done") || !strings.Contains(string(b), `"model"`) {
		t.Fatalf("uninstall:\n%s", b)
	}
	codex, _ = os.ReadFile(filepath.Join(h, ".codex", "hooks.json"))
	if strings.Contains(string(codex), "hooks") {
		t.Fatalf("empty hooks object left behind:\n%s", codex)
	}
	cursor, _ = os.ReadFile(filepath.Join(h, ".cursor", "hooks.json"))
	if strings.Contains(string(cursor), Arg) || !strings.Contains(string(cursor), "./audit.sh") {
		t.Fatalf("cursor uninstall:\n%s", cursor)
	}
}

func TestCursorSession(t *testing.T) {
	state := t.TempDir()
	now := time.Now()
	run := func(json string) {
		t.Helper()
		RunHook(state, harness.Cursor, strings.NewReader(json))
	}
	run(`{"hook_event_name":"beforeSubmitPrompt","conversation_id":"c1","workspace_roots":["/src/app"]}`)
	got := List(state, now)
	if len(got) != 1 || got[0].State != Running || got[0].Cwd != "/src/app" || got[0].Project() != "app" {
		t.Fatalf("prompt: %+v", got)
	}
	run(`{"hook_event_name":"postToolUse","conversation_id":"c1","cwd":"/src/app"}`)
	run(`{"hook_event_name":"afterAgentResponse","conversation_id":"c1","text":"All tests pass."}`)
	run(`{"hook_event_name":"stop","conversation_id":"c1","status":"completed"}`)
	got = List(state, now)
	if len(got) != 1 || got[0].State != Done || got[0].Message != "All tests pass." {
		t.Fatalf("stop keeps the reply: %+v", got[0])
	}
	run(`{"hook_event_name":"stop","conversation_id":"c2","status":"error"}`)
	got = List(state, now)
	if got[0].ID != "c2" || got[0].State != Done || got[0].Message != "Stopped on an error" {
		t.Fatalf("error stop: %+v", got[0])
	}
	run(`{"hook_event_name":"sessionEnd","session_id":"c1","reason":"user_close"}`)
	if got = List(state, now); len(got) != 1 || got[0].ID != "c2" {
		t.Fatalf("session end: %+v", got)
	}
}
