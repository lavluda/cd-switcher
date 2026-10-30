// Package ratelimits records the rate limits Claude Code itself reports.
//
// Claude Code passes a "rate_limits" object (five_hour and seven_day, each
// with used_percentage and resets_at) to its statusLine command after every
// response, for Pro and Max accounts. CD-Switcher installs itself as that
// command: it saves the reading per account, then runs whatever statusline the
// user had before with the same input and passes its output through. This is
// official data and costs no API call, unlike polling /api/oauth/usage.
package ratelimits

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Arg is the subcommand Claude Code runs.
const Arg = "statusline"

// Window is one limit.
type Window struct {
	Percent  float64
	ResetsAt time.Time
	Present  bool
}

// Snapshot is the latest reading for one account.
type Snapshot struct {
	AccountUUID string    `json:"accountUuid"`
	OrgUUID     string    `json:"organizationUuid"`
	Email       string    `json:"email,omitempty"`
	FiveHour    Window    `json:"fiveHour"`
	SevenDay    Window    `json:"sevenDay"`
	At          time.Time `json:"at"`
}

func snapshotPath(stateDir, account, org string) string {
	return filepath.Join(stateDir, "ratelimits", safe(account)+"_"+safe(org)+".json")
}

func wrappedPath(stateDir string) string { return filepath.Join(stateDir, "statusline-wrapped.json") }

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// RunHook is the statusline command. Recording is best effort and silent: a
// failure must never break the user's statusline.
func RunHook(stateDir string, in io.Reader, out io.Writer) {
	input, _ := io.ReadAll(io.LimitReader(in, 4<<20))
	record(stateDir, input, time.Now())
	b, err := os.ReadFile(wrappedPath(stateDir))
	if err != nil {
		return
	}
	var prev struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(b, &prev) != nil || prev.Command == "" {
		return
	}
	cmd := shellCommand(prev.Command)
	cmd.Stdin = strings.NewReader(string(input))
	cmd.Stdout = out
	_ = cmd.Run()
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("/bin/sh", "-c", command)
}

func record(stateDir string, input []byte, now time.Time) {
	type win struct {
		Used    *float64 `json:"used_percentage"`
		ResetAt int64    `json:"resets_at"`
	}
	var in struct {
		RateLimits *struct {
			FiveHour *win `json:"five_hour"`
			SevenDay *win `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(input, &in) != nil || in.RateLimits == nil {
		return
	}
	doc, err := cfgfile.ReadJSON(harness.ClaudeJSON())
	if err != nil {
		return
	}
	var acct struct {
		AccountUUID string `json:"accountUuid"`
		OrgUUID     string `json:"organizationUuid"`
		Email       string `json:"emailAddress"`
	}
	if doc.Get("oauthAccount", &acct) != nil || acct.AccountUUID == "" {
		return
	}
	conv := func(w *win) Window {
		if w == nil || w.Used == nil {
			return Window{}
		}
		v := Window{Percent: *w.Used, Present: true}
		if w.ResetAt > 0 {
			v.ResetsAt = time.Unix(w.ResetAt, 0)
		}
		return v
	}
	s := Snapshot{AccountUUID: acct.AccountUUID, OrgUUID: acct.OrgUUID, Email: acct.Email,
		FiveHour: conv(in.RateLimits.FiveHour), SevenDay: conv(in.RateLimits.SevenDay), At: now}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	p := snapshotPath(stateDir, s.AccountUUID, s.OrgUUID)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = cfgfile.WriteFile(p, b, true)
}

// Read returns the latest reading for an account, when Claude Code has
// reported one.
func Read(stateDir, account, org string) (Snapshot, bool) {
	b, err := os.ReadFile(snapshotPath(stateDir, account, org))
	if err != nil {
		return Snapshot{}, false
	}
	var s Snapshot
	if json.Unmarshal(b, &s) != nil {
		return Snapshot{}, false
	}
	return s, true
}

// Command is the statusLine command line that runs exe as the hook.
func Command(exe string) string {
	if runtime.GOOS == "windows" {
		return `"` + exe + `" ` + Arg
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' " + Arg
}

func isOurs(raw json.RawMessage) bool {
	var s struct {
		Command string `json:"command"`
	}
	return json.Unmarshal(raw, &s) == nil && strings.HasSuffix(s.Command, " "+Arg) && strings.Contains(strings.ToLower(s.Command), "cd-switcher")
}

// Installed reports whether Claude Code's statusLine runs CD-Switcher.
func Installed() bool {
	doc, err := cfgfile.ReadJSON(harness.ClaudeSettings())
	if err != nil {
		return false
	}
	raw, ok := doc.Raw("statusLine")
	return ok && isOurs(raw)
}

// Install makes exe Claude Code's statusLine command and keeps the previous
// statusline so the hook can keep running it.
func Install(stateDir, exe string) error {
	return cfgfile.UpdateJSON(harness.ClaudeSettings(), 0, false, func(doc *cfgfile.Object) (bool, error) {
		prev, had := doc.Raw("statusLine")
		if had && isOurs(prev) {
			prev, had = nil, false
			if b, err := os.ReadFile(wrappedPath(stateDir)); err == nil {
				prev, had = b, true
			}
		}
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return false, err
		}
		if had {
			if err := os.WriteFile(wrappedPath(stateDir), prev, 0o600); err != nil {
				return false, err
			}
		} else if err := os.Remove(wrappedPath(stateDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		next := cfgfile.NewObject()
		next.Set("type", "command")
		next.Set("command", Command(exe))
		var p struct {
			Padding *int `json:"padding"`
		}
		if had && json.Unmarshal(prev, &p) == nil && p.Padding != nil {
			next.Set("padding", *p.Padding)
		}
		return true, doc.Set("statusLine", next)
	})
}

// Uninstall puts the user's previous statusline back.
func Uninstall(stateDir string) error {
	return cfgfile.UpdateJSON(harness.ClaudeSettings(), 0, false, func(doc *cfgfile.Object) (bool, error) {
		cur, ok := doc.Raw("statusLine")
		if !ok || !isOurs(cur) {
			return false, nil
		}
		if b, err := os.ReadFile(wrappedPath(stateDir)); err == nil {
			doc.Set("statusLine", json.RawMessage(b))
			_ = os.Remove(wrappedPath(stateDir))
		} else {
			doc.Delete("statusLine")
		}
		return true, nil
	})
}
