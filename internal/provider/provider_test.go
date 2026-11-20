package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

func tempHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", "")
	os.MkdirAll(filepath.Join(h, ".claude"), 0o700)
	os.MkdirAll(filepath.Join(h, ".codex"), 0o700)
	return h
}

func TestClaudeSwitchAndBack(t *testing.T) {
	h := tempHome(t)
	settings := filepath.Join(h, ".claude", "settings.json")
	os.WriteFile(settings, []byte(`{"env":{"ANTHROPIC_MODEL":"mine","OTHER":"x"},"hooks":{"Stop":[]}}`), 0o644)
	var saved Saved

	or := Provider{Name: "OpenRouter", BaseURL: "https://openrouter.ai/api", KeyVar: "ANTHROPIC_AUTH_TOKEN", Env: map[string]string{"ANTHROPIC_API_KEY": ""}}
	if err := ApplyClaude(&or, "sk-or", &saved); err != nil {
		t.Fatal(err)
	}
	doc, _ := cfgfile.ReadJSON(settings)
	env, _ := doc.Child("env")
	var tok, model, other string
	env.Get("ANTHROPIC_AUTH_TOKEN", &tok)
	env.Get("ANTHROPIC_MODEL", &model)
	env.Get("OTHER", &other)
	if tok != "sk-or" || model != "" || other != "x" || !env.Has("ANTHROPIC_API_KEY") || !doc.Has("hooks") {
		t.Fatalf("after switch: %s", mustJSON(doc))
	}
	if fi, _ := os.Stat(settings); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings with a key should be owner-only, mode %v", fi.Mode().Perm())
	}
	if ActiveClaude() != "https://openrouter.ai/api" {
		t.Fatal("ActiveClaude does not see the provider")
	}

	local := Provider{Name: "Ollama", BaseURL: "http://localhost:11434"}
	if err := ApplyClaude(&local, "ollama", &saved); err != nil {
		t.Fatal(err)
	}
	if err := ApplyClaude(nil, "", &saved); err != nil {
		t.Fatal(err)
	}
	doc, _ = cfgfile.ReadJSON(settings)
	env, _ = doc.Child("env")
	model = ""
	env.Get("ANTHROPIC_MODEL", &model)
	if model != "mine" || env.Has("ANTHROPIC_BASE_URL") || env.Has("ANTHROPIC_AUTH_TOKEN") || env.Has("ANTHROPIC_API_KEY") {
		t.Fatalf("official not restored: %s", mustJSON(doc))
	}
	if saved.ClaudeEnv != nil {
		t.Fatal("saved official values not cleared after restoring them")
	}
}

func TestCodexSwitchAndBack(t *testing.T) {
	h := tempHome(t)
	cfg := filepath.Join(h, ".codex", "config.toml")
	orig := "model = \"gpt-6\"\n\n[mcp_servers.x]\ncommand = \"y\"\n"
	os.WriteFile(cfg, []byte(orig), 0o600)
	var saved Saved

	p := Provider{Name: "Local", Harness: harness.Codex, BaseURL: "http://localhost:11434/v1", Model: "qwen3"}
	if err := ApplyCodex(&p, "k", &saved); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	s := string(b)
	for _, want := range []string{`model = "qwen3"`, `model_provider = "cdswitcher"`, `[model_providers.cdswitcher]`, `base_url = "http://localhost:11434/v1"`, `wire_api = "responses"`, `experimental_bearer_token = "k"`, "[mcp_servers.x]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if !ActiveCodex() {
		t.Fatal("ActiveCodex false after switch")
	}
	if err := ApplyCodex(nil, "", &saved); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cfg)
	if string(b) != orig {
		t.Fatalf("not restored:\n%s", b)
	}
}

func TestKeys(t *testing.T) {
	k := Keys{Path: filepath.Join(t.TempDir(), "keys.json")}
	if err := k.Set("a", "secret"); err != nil {
		t.Fatal(err)
	}
	if k.Get("a") != "secret" {
		t.Fatal("key not stored")
	}
	if fi, _ := os.Stat(k.Path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	k.Set("a", "")
	if k.Get("a") != "" {
		t.Fatal("key not forgotten")
	}
}

func mustJSON(o *cfgfile.Object) string {
	b, _ := o.MarshalJSON()
	return string(b)
}
