package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadLogTailNewestFirstWholeLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(p, []byte("aaaa\nbb\ncc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readLogTail(p, 100); got != "cc\nbb\naaaa" {
		t.Errorf("full = %q", got)
	}
	if got := readLogTail(p, 8); got != "cc\nbb" { // cut lands inside "aaaa"
		t.Errorf("tail = %q", got)
	}
	if got := readLogTail(filepath.Join(t.TempDir(), "missing"), 8); got != "" {
		t.Errorf("missing = %q", got)
	}
}

func TestParseLogAndDays(t *testing.T) {
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.Local)
	got := parseLog("2026/11/12 14:31:05 switch: Work to Personal\nmore output\n2026/11/11 09:00:00 update: ok\ncut line\n\n")
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	if got[1].msg != "update: ok\nmore output" {
		t.Errorf("continuation not attached: %q", got[1].msg)
	}
	if got[0].msg != "switch: Work to Personal" || logDay(got[0].when, now) != "Today" {
		t.Errorf("first = %+v / %s", got[0], logDay(got[0].when, now))
	}
	if logDay(got[1].when, now) != "Yesterday" {
		t.Errorf("second day = %s", logDay(got[1].when, now))
	}
	if !got[2].when.IsZero() || got[2].msg != "cut line" || logDay(got[2].when, now) != "Earlier" {
		t.Errorf("untimed = %+v", got[2])
	}
}
