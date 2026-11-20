package cctoggles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestToggles(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	os.MkdirAll(filepath.Join(h, ".claude"), 0o700)
	for _, tg := range All {
		if err := Set(tg.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	for id, on := range State() {
		if !on {
			t.Fatalf("%s off after turning it on", id)
		}
	}
	for _, tg := range All {
		Set(tg.ID, false)
	}
	for id, on := range State() {
		if on {
			t.Fatalf("%s still on", id)
		}
	}
	b, _ := os.ReadFile(filepath.Join(h, ".claude", "settings.json"))
	if string(b) != "{}\n" {
		t.Fatalf("leftovers: %s", b)
	}
}
