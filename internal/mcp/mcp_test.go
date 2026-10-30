package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

func setup(t *testing.T) (h, desktop string) {
	t.Helper()
	h = t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	desktop = filepath.Join(h, "Desktop")
	for _, d := range []string{".claude", ".codex", ".cursor", ".config/opencode", desktop} {
		os.MkdirAll(filepath.Join(h, strings.TrimPrefix(d, h)), 0o700)
	}
	os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"numStartups":3,"mcpServers":{"fs":{"type":"stdio","command":"npx","args":["-y","fs"],"env":{"A":"1"}}}}`), 0o600)
	os.WriteFile(filepath.Join(h, ".codex", "config.toml"), []byte("model = \"x\"\n\n[mcp_servers.fs]\ncommand = \"npx\"\nargs = [\"-y\", \"fs\"]\nstartup_timeout_sec = 30\n\n[mcp_servers.remote]\nurl = \"https://r/mcp\"\n"), 0o600)
	os.WriteFile(filepath.Join(h, ".config", "opencode", "opencode.json"), []byte(`{"theme":"x"}`), 0o644)
	return h, desktop
}

func TestImportMergesAndApplyAddsRemoves(t *testing.T) {
	h, desktop := setup(t)
	servers, errs := Import(desktop)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(servers) != 2 || servers[0].Name != "fs" || !servers[0].Enabled[harness.Claude] || !servers[0].Enabled[harness.Codex] || servers[1].Name != "remote" || !servers[1].Remote() {
		t.Fatalf("import = %+v", servers)
	}

	// Turn fs on everywhere, remote off in Codex, and add a new one.
	for _, tool := range Tools {
		servers[0].Enabled[tool] = true
	}
	servers[1].Enabled[harness.Codex] = false
	servers[1].Enabled[harness.Cursor] = true
	servers[1].Enabled[harness.Desktop] = true // unsupported: must be skipped
	if errs := Apply(desktop, servers, nil, nil); len(errs) > 0 {
		t.Fatal(errs)
	}

	codex, _ := os.ReadFile(filepath.Join(h, ".codex", "config.toml"))
	if !strings.Contains(string(codex), "startup_timeout_sec = 30") {
		t.Fatalf("an unedited Codex entry lost its extras:\n%s", codex)
	}
	if strings.Contains(string(codex), "[mcp_servers.remote]") {
		t.Fatalf("disabled server still in Codex:\n%s", codex)
	}
	oc, _ := cfgfile.ReadJSON(filepath.Join(h, ".config", "opencode", "opencode.json"))
	mcpObj, _ := oc.Child("mcp")
	var fs struct {
		Type    string   `json:"type"`
		Command []string `json:"command"`
	}
	mcpObj.Get("fs", &fs)
	if fs.Type != "local" || strings.Join(fs.Command, " ") != "npx -y fs" || !oc.Has("theme") {
		t.Fatalf("opencode = %s", mustJSON(oc))
	}
	desk, _ := cfgfile.ReadJSON(filepath.Join(desktop, "claude_desktop_config.json"))
	dm, _ := desk.Child("mcpServers")
	if !dm.Has("fs") || dm.Has("remote") {
		t.Fatalf("desktop = %s", mustJSON(desk))
	}
	cur, _ := cfgfile.ReadJSON(filepath.Join(h, ".cursor", "mcp.json"))
	cm, _ := cur.Child("mcpServers")
	var r struct {
		URL string `json:"url"`
	}
	cm.Get("remote", &r)
	if r.URL != "https://r/mcp" {
		t.Fatalf("cursor = %s", mustJSON(cur))
	}

	// Editing fs rewrites it; removing it deletes it everywhere.
	servers[0].Args = []string{"-y", "fs@2"}
	Apply(desktop, servers, map[string]bool{"fs": true}, nil)
	codex, _ = os.ReadFile(filepath.Join(h, ".codex", "config.toml"))
	if !strings.Contains(string(codex), `"fs@2"`) {
		t.Fatalf("edit not written:\n%s", codex)
	}
	Apply(desktop, servers[1:], nil, map[string]bool{"fs": true})
	cj, _ := cfgfile.ReadJSON(filepath.Join(h, ".claude.json"))
	cs, _ := cj.Child("mcpServers")
	codex, _ = os.ReadFile(filepath.Join(h, ".codex", "config.toml"))
	if cs.Has("fs") || strings.Contains(string(codex), "mcp_servers.fs") || !cj.Has("numStartups") {
		t.Fatalf("remove failed: %s\n%s", mustJSON(cj), codex)
	}
}

func TestParsePasted(t *testing.T) {
	got, err := ParsePasted(`{"mcpServers":{"a":{"command":"npx","args":["x"]},"b":{"type":"sse","url":"https://b"}}}`)
	if err != nil || len(got) != 2 || got[0].Command != "npx" || got[1].Transport != "sse" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ParsePasted("not json"); err == nil {
		t.Fatal("expected an error")
	}
}

func mustJSON(o *cfgfile.Object) string {
	b, _ := o.MarshalJSON()
	return string(b)
}
