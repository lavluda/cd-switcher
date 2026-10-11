package claude

import (
	"testing"
	"time"
)

func TestParseLastUpdateQuit(t *testing.T) {
	log := "2026-10-10 07:39:29 [info] Starting app {\n" +
		"2026-10-10 07:51:43 [info] beforeQuitForUpdate handler fired, going down for update\n" +
		"2026-10-10 07:51:43 [info] beforeQuitForUpdate handler fired, going down for update\n" +
		"2026-10-10 09:38:46 [info] beforeQuitForUpdate handler fired, going down for update\n" +
		"2026-10-10 09:40:00 [info] beforeQuit: handler fired, going down\n"
	got, ok := parseLastUpdateQuit(log, time.UTC)
	want := time.Date(2026, 10, 10, 9, 38, 46, 0, time.UTC)
	if !ok || !got.Equal(want) {
		t.Fatalf("got %v %v, want %v", got, ok, want)
	}
}

func TestParseLastUpdateQuitIgnoresPlainQuit(t *testing.T) {
	log := "2026-10-10 07:38:31 [info] beforeQuit: handler fired, going down\n"
	if _, ok := parseLastUpdateQuit(log, time.UTC); ok {
		t.Fatal("a plain quit must not count as an update restart")
	}
}
