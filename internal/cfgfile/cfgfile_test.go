package cfgfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateJSONKeepsOrderAndOtherKeys(t *testing.T) {
	dir := t.TempDir()
	BackupDir = filepath.Join(dir, "backups")
	defer func() { BackupDir = "" }()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	os.WriteFile(real, []byte(`{"zeta":1,"env":{"B":"2","A":"1"},"alpha":true}`), 0o640)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks need extra rights here:", err)
	}

	err := UpdateJSON(link, 0, false, func(doc *Object) (bool, error) {
		env, err := doc.Child("env")
		if err != nil {
			return false, err
		}
		env.Delete("B")
		env.Set("C", "3")
		return true, doc.Set("env", env)
	})
	if err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	b, _ := os.ReadFile(real)
	got := strings.Join(strings.Fields(string(b)), "")
	if got != `{"zeta":1,"env":{"A":"1","C":"3"},"alpha":true}` {
		t.Fatalf("got %s", got)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if fi, _ := os.Stat(real); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	orig, err := os.ReadFile(filepath.Join(BackupDir, strings.ReplaceAll(strings.TrimPrefix(real, "/"), "/", "_")+".orig"))
	if err != nil || !strings.Contains(string(orig), `"B":"2"`) {
		t.Fatalf("first-write backup missing: %v", err)
	}
}

func TestUpdateJSONRefusesUnparseable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonc")
	os.WriteFile(p, []byte("{ // comment\n}"), 0o644)
	called := false
	err := UpdateJSON(p, 0, false, func(*Object) (bool, error) { called = true; return true, nil })
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestTOMLEdits(t *testing.T) {
	in := `# my codex config
model = "gpt-6"

[mcp_servers.a]
command = "x"

[mcp_servers.a.env]
K = "v"

# comment for b
[mcp_servers."b c"]
url = "https://b"

[features]
x = true
`
	out := SetTOMLRootKey(in, "model_provider", `"cdswitcher"`)
	out = SetTOMLRootKey(out, "model", `"local"`)
	out = SetTOMLTable(out, []string{"mcp_servers", "a"}, "")
	out = SetTOMLTable(out, []string{"mcp_servers", "b c"}, TableHeader("mcp_servers", "b c")+"\nurl = \"https://new\"\n")
	out = SetTOMLTable(out, []string{"model_providers", "cdswitcher"}, TableHeader("model_providers", "cdswitcher")+"\nbase_url = \"http://x\"\n")
	want := `# my codex config
model = "local"
model_provider = "cdswitcher"

# comment for b
[mcp_servers."b c"]
url = "https://new"

[features]
x = true

[model_providers.cdswitcher]
base_url = "http://x"
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if v, ok := TOMLRootValue(out, "model"); !ok || v != `"local"` {
		t.Fatalf("root value = %q %v", v, ok)
	}
	if back := SetTOMLRootKey(out, "model_provider", ""); strings.Contains(back, "model_provider =") {
		t.Fatal("root key not removed")
	}
	if got := TOMLInline(map[string]string{"B": "2", "a b": `q"`}); got != `{ B = "2", "a b" = "q\"" }` {
		t.Fatalf("inline = %s", got)
	}
}
