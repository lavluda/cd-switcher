package sessionlog

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestScanCountsOncePricesAndAppends(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	ts := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	claudeLine := `{"type":"assistant","timestamp":"` + ts + `","requestId":"r1","cwd":"/src/app/.claude/worktrees/x","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_read_input_tokens":1000000,"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_5m_input_tokens":1000000,"ephemeral_1h_input_tokens":0}}}}` + "\n"
	cdir := filepath.Join(h, ".claude", "projects", "p")
	os.MkdirAll(cdir, 0o700)
	// The same response twice in one file (two content blocks) and once more
	// in a resumed session's file.
	os.WriteFile(filepath.Join(cdir, "a.jsonl"), []byte(claudeLine+claudeLine), 0o600)
	os.WriteFile(filepath.Join(cdir, "b.jsonl"), []byte(claudeLine), 0o600)

	xdir := filepath.Join(h, ".codex", "sessions", "2026", "10", "30")
	os.MkdirAll(xdir, 0o700)
	codex := `{"timestamp":"` + ts + `","type":"session_meta","payload":{"cwd":"/src/api"}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1},"last_token_usage":{"input_tokens":999}}}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"token_usage_record","payload":{"response_id":"x1","usage":{"input_tokens":3000000,"cached_input_tokens":1000000,"output_tokens":1000000}}}` + "\n"
	xfile := filepath.Join(xdir, "rollout.jsonl")
	os.WriteFile(xfile, []byte(codex), 0o600)

	s := NewScanner()
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	r := s.Report(midnight, "", true)
	if r.Total.Requests != 2 {
		t.Fatalf("requests = %d, want 2 (duplicates must count once)", r.Total.Requests)
	}
	// Opus 5.5: 4 in + 20 out + 0.20 read + 5 write; Sol: 2*2 in + 10 out + 0.10 read.
	if want := 4 + 20 + 0.20 + 5 + 4 + 10 + 0.10; math.Abs(r.Total.Cost-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", r.Total.Cost, want)
	}
	if len(r.ByProject) != 2 || (r.ByProject[0].Name != "app" && r.ByProject[1].Name != "app") {
		t.Fatalf("projects = %+v (worktree should count under app)", r.ByProject)
	}
	if got := s.Report(midnight, Codex, true).Total.CacheRead; got != 1000000 {
		t.Fatalf("codex cache read = %d", got)
	}

	// Appended lines are picked up; old ones are not read again.
	f, _ := os.OpenFile(xfile, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"timestamp":"` + ts + `","type":"token_usage_record","payload":{"response_id":"x2","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n")
	f.Close()
	s.Scan()
	if r := s.Report(midnight, Codex, true); r.Total.Requests != 2 {
		t.Fatalf("after append, codex requests = %d, want 2", r.Total.Requests)
	}
}
