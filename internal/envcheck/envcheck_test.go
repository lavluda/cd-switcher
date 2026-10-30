package envcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestScanAndDisable(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); watched.MatchString(name) {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	rc := filepath.Join(h, ".zshrc")
	os.WriteFile(rc, []byte("export PATH=/x\nexport ANTHROPIC_BASE_URL=https://proxy\n# export OPENAI_API_KEY=old\nCODEX_HOME=~/.c\n"), 0o644)
	fish := filepath.Join(h, ".config", "fish", "config.fish")
	os.MkdirAll(filepath.Dir(fish), 0o755)
	os.WriteFile(fish, []byte("set -gx OPENAI_BASE_URL http://x\n"), 0o644)

	got := Scan()
	var names []string
	for _, f := range got {
		names = append(names, f.String())
	}
	want := []string{
		"ANTHROPIC_BASE_URL in " + rc + " line 2",
		"CODEX_HOME in " + rc + " line 4",
		"OPENAI_BASE_URL in " + fish + " line 1",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", names)
	}

	if err := Disable(got[0], filepath.Join(h, "backups")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(rc)
	if !strings.Contains(string(b), "# disabled by CD-Switcher: export ANTHROPIC_BASE_URL") {
		t.Fatalf("not disabled:\n%s", b)
	}
	if err := Disable(got[0], filepath.Join(h, "backups")); err == nil {
		t.Fatal("disabling a line that changed must fail")
	}
	if len(Scan()) != 2 {
		t.Fatal("disabled line still reported")
	}
}
