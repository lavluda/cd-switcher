// Package copilot reads GitHub Copilot's quota with the login the Copilot
// editor plugins already saved on this machine. Nothing is signed in here.
package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

var (
	userURL    = "https://api.github.com/copilot_internal/user"
	httpClient = &http.Client{Timeout: 15 * time.Second}
)

// Quota is the premium-request allowance, which is the limit that runs out.
type Quota struct {
	Login     string
	Plan      string
	Percent   float64 // used, 0-100
	Remaining int
	Total     int
	Unlimited bool
	ResetsAt  time.Time
}

// ErrNoLogin means no Copilot plugin login was found on this machine.
var ErrNoLogin = errors.New("no GitHub Copilot login on this machine")

// tokens lists the oauth tokens in apps.json and hosts.json, newest format
// first. Several apps can each hold one; some may have been revoked.
func tokens() []string {
	dir := filepath.Join(harness.Home(), ".config", "github-copilot")
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		dir = filepath.Join(d, "github-copilot")
	}
	var out []string
	for _, f := range []string{"apps.json", "hosts.json"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		var m map[string]struct {
			Token string `json:"oauth_token"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if m[k].Token != "" {
				out = append(out, m[k].Token)
			}
		}
	}
	return out
}

// Fetch returns the quota from the first saved token GitHub accepts.
func Fetch(ctx context.Context) (Quota, error) {
	toks := tokens()
	if len(toks) == 0 {
		return Quota{}, ErrNoLogin
	}
	var lastErr error
	for _, t := range toks {
		q, err := fetch(ctx, t)
		if err == nil {
			return q, nil
		}
		lastErr = err
	}
	return Quota{}, lastErr
}

func fetch(ctx context.Context, token string) (Quota, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.99.0")
	req.Header.Set("User-Agent", "GithubCopilot/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Quota{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return Quota{}, fmt.Errorf("copilot: status %d", resp.StatusCode)
	}
	return parse(b)
}

func parse(b []byte) (Quota, error) {
	var r struct {
		Login     string `json:"login"`
		Plan      string `json:"copilot_plan"`
		Snapshots map[string]struct {
			PercentRemaining float64 `json:"percent_remaining"`
			Remaining        int     `json:"remaining"`
			Entitlement      int     `json:"entitlement"`
			Unlimited        bool    `json:"unlimited"`
			ResetAt          int64   `json:"quota_reset_at"`
		} `json:"quota_snapshots"`
		ResetDate string `json:"quota_reset_date_utc"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Quota{}, err
	}
	s, ok := r.Snapshots["premium_interactions"]
	if !ok {
		return Quota{}, errors.New("copilot: no premium request quota in reply")
	}
	q := Quota{Login: r.Login, Plan: r.Plan, Percent: 100 - s.PercentRemaining, Remaining: s.Remaining, Total: s.Entitlement, Unlimited: s.Unlimited}
	if s.ResetAt > 0 {
		q.ResetsAt = time.Unix(s.ResetAt, 0)
	} else if t, err := time.Parse(time.RFC3339, r.ResetDate); err == nil {
		q.ResetsAt = t
	}
	return q, nil
}
