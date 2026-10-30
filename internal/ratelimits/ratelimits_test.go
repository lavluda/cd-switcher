package ratelimits

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

func setup(t *testing.T) (h, state string) {
	t.Helper()
	h, state = t.TempDir(), t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	os.MkdirAll(filepath.Join(h, ".claude"), 0o700)
	os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc","organizationUuid":"org","emailAddress":"a@x"}}`), 0o600)
	return h, state
}

func TestInstallWrapsPreviousStatuslineAndRecords(t *testing.T) {
	h, state := setup(t)
	settings := filepath.Join(h, ".claude", "settings.json")
	os.WriteFile(settings, []byte(`{"model":"opus","statusLine":{"type":"command","command":"echo mine","padding":2}}`), 0o644)

	exe := "/Applications/CD-Switcher.app/Contents/MacOS/cd-switcher"
	if err := Install(state, exe); err != nil {
		t.Fatal(err)
	}
	if !Installed() {
		t.Fatal("not reported as installed")
	}
	if err := Install(state, exe); err != nil { // twice must not wrap itself
		t.Fatal(err)
	}
	doc, _ := cfgfile.ReadJSON(settings)
	var sl struct {
		Command string `json:"command"`
		Padding int    `json:"padding"`
	}
	doc.Get("statusLine", &sl)
	if sl.Command != Command(exe) || sl.Padding != 2 {
		t.Fatalf("statusLine = %+v", sl)
	}

	var out bytes.Buffer
	RunHook(state, strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":42.5,"resets_at":1900000000},"seven_day":{"used_percentage":10,"resets_at":1900500000}}}`), &out)
	if strings.TrimSpace(out.String()) != "mine" {
		t.Fatalf("previous statusline output = %q", out.String())
	}
	s, ok := Read(state, "acc", "org")
	if !ok || s.FiveHour.Percent != 42.5 || !s.SevenDay.Present || s.SevenDay.ResetsAt.Unix() != 1900500000 || s.Email != "a@x" {
		t.Fatalf("snapshot = %+v ok=%v", s, ok)
	}

	if err := Uninstall(state); err != nil {
		t.Fatal(err)
	}
	doc, _ = cfgfile.ReadJSON(settings)
	doc.Get("statusLine", &sl)
	if sl.Command != "echo mine" || Installed() {
		t.Fatalf("previous statusline not restored: %+v", sl)
	}
	var model string
	doc.Get("model", &model)
	if model != "opus" {
		t.Fatal("unrelated setting lost")
	}
}

func TestHookWithoutLimitsRecordsNothing(t *testing.T) {
	_, state := setup(t)
	RunHook(state, strings.NewReader(`{"model":{"id":"x"}}`), &bytes.Buffer{})
	if _, ok := Read(state, "acc", "org"); ok {
		t.Fatal("recorded a reading with no rate_limits")
	}
}
