// Package codex manages OpenAI Codex CLI logins: one saved auth.json per
// account, swapped into $CODEX_HOME/auth.json on switch, plus each account's
// usage limits from ChatGPT's backend.
//
// The live login is auth.json by default, or a keychain item when Codex is
// set to keep it there (see live.go).
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/fslock"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Codex's public OAuth client and endpoints (codex-rs/login/src/auth/manager.rs).
const (
	clientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

var (
	tokenURL   = "https://auth.openai.com/oauth/token"
	usageURL   = "https://chatgpt.com/backend-api/wham/usage"
	httpClient = &http.Client{Timeout: 20 * time.Second}
)

// Identity is who an auth.json belongs to.
type Identity struct {
	Email     string
	Plan      string
	AccountID string
}

// LivePath is the auth.json Codex reads.
func LivePath() string { return filepath.Join(harness.CodexHome(), "auth.json") }

// ReadIdentity reads an auth.json and says whose it is.
func ReadIdentity(path string) (Identity, error) {
	doc, err := readAuth(path)
	if err != nil {
		return Identity{}, err
	}
	return identityOf(doc), nil
}

func readAuth(path string) (*cfgfile.Object, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return cfgfile.ParseObject(b)
}

type tokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

func tokensOf(doc *cfgfile.Object) tokens {
	var t tokens
	_ = doc.Get("tokens", &t)
	return t
}

func identityOf(doc *cfgfile.Object) Identity {
	t := tokensOf(doc)
	var claims struct {
		Email string `json:"email"`
		Auth  struct {
			Plan      string `json:"chatgpt_plan_type"`
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	_ = jwtClaims(t.IDToken, &claims)
	id := Identity{Email: claims.Email, Plan: claims.Auth.Plan, AccountID: t.AccountID}
	if id.AccountID == "" {
		id.AccountID = claims.Auth.AccountID
	}
	return id
}

// jwtClaims decodes a JWT's payload without checking its signature: the token
// came from the user's own disk and is only read for display.
func jwtClaims(token string, v any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("not a JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func expiry(token string) time.Time {
	var c struct {
		Exp int64 `json:"exp"`
	}
	if jwtClaims(token, &c) != nil || c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

// Copy copies one auth.json to another under Codex's auth.lock, owner-only.
// Codex rewrites auth.json in place on refresh; the lock keeps the copy whole.
func Copy(src, dst string) error {
	l, err := fslock.LockFile(filepath.Join(harness.CodexHome(), "auth.lock"), 10*time.Second)
	if err != nil {
		return err
	}
	defer l.Unlock()
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if _, err := cfgfile.ParseObject(b); err != nil {
		return fmt.Errorf("%s is not a Codex login: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return cfgfile.WriteFile(dst, b, true)
}

// Login runs `codex login` with a throwaway CODEX_HOME and returns the
// auth.json it wrote. Codex opens the browser itself. The command runs in the
// user's login shell so it is found the same way a terminal finds it.
func Login(ctx context.Context) (authPath string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "cd-switcher-codex-login-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.CommandContext(ctx, shell, "-lc", `codex -c 'cli_auth_credentials_store="file"' login`)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("codex login: %v: %s", err, lastLine(out.String()))
	}
	p := filepath.Join(dir, "auth.json")
	if _, err := os.Stat(p); err != nil {
		cleanup()
		return "", nil, errors.New("codex login finished without writing a login")
	}
	return p, cleanup, nil
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(ls[len(ls)-1])
}

// Window is one usage limit.
type Window struct {
	Percent  float64
	ResetsAt time.Time
	Seconds  int
}

// Quota is an account's limits as ChatGPT reports them.
type Quota struct {
	Email   string
	Plan    string
	Session Window // the 5-hour window, when present
	Weekly  Window // the 7-day window, when present
	Limited bool
}

// Usage fetches an account's limits. canRefresh allows refreshing an expired
// access token and writing the rotated tokens back into authPath; pass false
// for the live auth.json, which Codex itself keeps fresh.
func Usage(ctx context.Context, authPath string, canRefresh bool) (Quota, error) {
	doc, err := readAuth(authPath)
	if err != nil {
		return Quota{}, err
	}
	t := tokensOf(doc)
	if t.AccessToken == "" {
		return Quota{}, errors.New("no ChatGPT login")
	}
	if exp := expiry(t.AccessToken); !exp.IsZero() && time.Until(exp) < 2*time.Minute {
		if !canRefresh || t.RefreshToken == "" {
			return Quota{}, errors.New("login expired; run codex once to refresh it")
		}
		if t, err = refresh(ctx, authPath, doc, t); err != nil {
			return Quota{}, err
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	if t.AccountID != "" {
		req.Header.Set("chatgpt-account-id", t.AccountID)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs")
	body, err := do(req)
	if err != nil {
		return Quota{}, err
	}
	return parseUsage(body)
}

func parseUsage(body []byte) (Quota, error) {
	type win struct {
		Used    float64 `json:"used_percent"`
		Seconds int     `json:"limit_window_seconds"`
		ResetAt int64   `json:"reset_at"`
	}
	var r struct {
		Email     string `json:"email"`
		Plan      string `json:"plan_type"`
		RateLimit struct {
			LimitReached bool `json:"limit_reached"`
			Primary      *win `json:"primary_window"`
			Secondary    *win `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Quota{}, fmt.Errorf("codex usage: %w", err)
	}
	q := Quota{Email: r.Email, Plan: r.Plan, Limited: r.RateLimit.LimitReached}
	// Classify by window length, not position: a weekly-only answer can come
	// back as the primary window.
	for _, w := range []*win{r.RateLimit.Primary, r.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		v := Window{Percent: w.Used, Seconds: w.Seconds}
		if w.ResetAt > 0 {
			v.ResetsAt = time.Unix(w.ResetAt, 0)
		}
		if w.Seconds >= 24*3600 {
			q.Weekly = v
		} else {
			q.Session = v
		}
	}
	return q, nil
}

func refresh(ctx context.Context, authPath string, doc *cfgfile.Object, t tokens) (tokens, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id":     clientID,
		"grant_type":    "refresh_token",
		"refresh_token": t.RefreshToken,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out, err := do(req)
	if err != nil {
		return t, fmt.Errorf("refresh codex login: %w", err)
	}
	var r struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.AccessToken == "" {
		return t, errors.New("refresh codex login: no access token in reply")
	}
	if r.IDToken != "" {
		t.IDToken = r.IDToken
	}
	t.AccessToken = r.AccessToken
	if r.RefreshToken != "" {
		t.RefreshToken = r.RefreshToken
	}
	// Write the rotated tokens back before anything else: the old refresh
	// token stops working the moment the server rotates it.
	tok, err := doc.Child("tokens")
	if err != nil {
		return t, err
	}
	tok.Set("id_token", t.IDToken)
	tok.Set("access_token", t.AccessToken)
	tok.Set("refresh_token", t.RefreshToken)
	doc.Set("tokens", tok)
	doc.Set("last_refresh", time.Now().UTC().Format(time.RFC3339Nano))
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return t, err
	}
	return t, cfgfile.WriteFile(authPath, b, true)
}

func do(req *http.Request) ([]byte, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return b, nil
}
