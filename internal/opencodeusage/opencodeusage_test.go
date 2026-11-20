package opencodeusage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestRead(t *testing.T) {
	if sqliteBin() == "" {
		t.Skip("sqlite3 not installed")
	}
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("XDG_DATA_HOME", "")
	dir := filepath.Join(h, ".local", "share", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "opencode.db")
	sql := `CREATE TABLE session_v2 (time_updated integer, cost real, tokens_input integer, tokens_output integer, tokens_reasoning integer, tokens_cache_read integer, tokens_cache_write integer, directory text, model text);
INSERT INTO session_v2 VALUES (1792824652853, 1.25, 1000, 20, 5, 40, 10, '/src/genesis', '{"id":"nemotron","providerID":"opencode"}');`
	cmd := exec.Command(sqliteBin(), db, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	rows, err := Read(time.UnixMilli(1792824652853).Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "nemotron" || rows[0].Project != "genesis" || rows[0].Cost != 1.25 || rows[0].Input != 1000 || rows[0].Output != 25 || rows[0].CacheRead != 40 {
		t.Fatalf("%+v", rows)
	}
}
