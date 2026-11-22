//go:build linux

package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableDisable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Enable(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "autostart", entryName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nExec=\"") || !strings.HasPrefix(string(data), "[Desktop Entry]\n") {
		t.Fatalf("unexpected entry:\n%s", data)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("entry still present: %v", err)
	}
	if err := Disable(); err != nil {
		t.Fatalf("second Disable: %v", err)
	}
}

func TestExecQuote(t *testing.T) {
	got := execQuote(`/opt/a b/$x"` + "`" + `y\z`)
	want := `"/opt/a b/\$x\"\` + "`" + `y\\\\z"`
	if got != want {
		t.Fatalf("execQuote = %s, want %s", got, want)
	}
}
