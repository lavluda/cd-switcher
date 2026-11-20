package instructions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestExistingAndApply(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	os.MkdirAll(filepath.Join(h, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(h, ".codex"), 0o755)
	os.MkdirAll(filepath.Join(h, ".gemini"), 0o755)
	os.WriteFile(filepath.Join(h, ".claude", "CLAUDE.md"), []byte("be brief\n"), 0o644)
	os.WriteFile(filepath.Join(h, ".codex", "AGENTS.md"), []byte("be brief\n"), 0o644)
	os.WriteFile(filepath.Join(h, ".gemini", "GEMINI.md"), []byte("other\n"), 0o644)

	ex := Existing()
	if len(ex) != 2 || ex[0].Name != "From Claude Code, Codex" || ex[1].Name != "From Gemini CLI" {
		t.Fatalf("existing = %+v", ex)
	}
	if errs := Apply("one rule", map[string]bool{harness.Claude: true, harness.Gemini: true}); len(errs) > 0 {
		t.Fatal(errs)
	}
	sync := InSync("one rule")
	if !sync[harness.Claude] || !sync[harness.Gemini] || sync[harness.Codex] {
		t.Fatalf("in sync = %v", sync)
	}
}
