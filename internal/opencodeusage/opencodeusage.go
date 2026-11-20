// Package opencodeusage reads token and cost totals OpenCode stores in its
// local database. Nothing is sent anywhere.
package opencodeusage

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

// Row is one OpenCode session.
type Row struct {
	At         time.Time
	Model      string
	Project    string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
}

// Read returns sessions updated since the given time. A missing database is
// an empty result.
func Read(since time.Time) ([]Row, error) {
	db := filepath.Join(dataDir(), "opencode.db")
	if _, err := os.Stat(db); err != nil {
		return nil, nil
	}
	bin := sqliteBin()
	if bin == "" {
		return nil, errors.New("opencode usage: sqlite3 is not installed")
	}
	q := `SELECT time_updated, cost, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write, directory, IFNULL(model,'') AS model FROM session_v2 WHERE time_updated >= ` + strconv.FormatInt(since.UnixMilli(), 10)
	cmd := exec.Command(bin, "-readonly", "-json", db, q)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil, err
	}
	var raw []struct {
		Time   int64   `json:"time_updated"`
		Cost   float64 `json:"cost"`
		In     int64   `json:"tokens_input"`
		Out    int64   `json:"tokens_output"`
		Reason int64   `json:"tokens_reasoning"`
		Read   int64   `json:"tokens_cache_read"`
		Write  int64   `json:"tokens_cache_write"`
		Dir    string  `json:"directory"`
		Model  string  `json:"model"`
	}
	if json.Unmarshal(out, &raw) != nil {
		// sqlite3 names the expression column differently across versions.
		return parseLoose(out), nil
	}
	rows := make([]Row, 0, len(raw))
	for _, r := range raw {
		rows = append(rows, Row{
			At: time.UnixMilli(r.Time), Model: modelName(r.Model), Project: filepath.Base(r.Dir),
			Input: r.In, Output: r.Out + r.Reason, CacheRead: r.Read, CacheWrite: r.Write, Cost: r.Cost,
		})
	}
	return rows, nil
}

func parseLoose(out []byte) []Row {
	var raw []map[string]any
	if json.Unmarshal(out, &raw) != nil {
		return nil
	}
	rows := make([]Row, 0, len(raw))
	for _, m := range raw {
		at := int64(num(m["time_updated"]))
		model, _ := m["model"].(string)
		dir, _ := m["directory"].(string)
		rows = append(rows, Row{
			At: time.UnixMilli(at), Model: modelName(model), Project: filepath.Base(dir),
			Input:      int64(num(m["tokens_input"])),
			Output:     int64(num(m["tokens_output"]) + num(m["tokens_reasoning"])),
			CacheRead:  int64(num(m["tokens_cache_read"])),
			CacheWrite: int64(num(m["tokens_cache_write"])),
			Cost:       num(m["cost"]),
		})
	}
	return rows
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func modelName(raw string) string {
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &m) == nil && m.ID != "" {
		return m.ID
	}
	if raw == "" {
		return "opencode"
	}
	return raw
}

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	return filepath.Join(harness.Home(), ".local", "share", "opencode")
}

func sqliteBin() string {
	for _, p := range []string{"/usr/bin/sqlite3", "/opt/homebrew/bin/sqlite3"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("sqlite3"); err == nil {
		return p
	}
	return ""
}
