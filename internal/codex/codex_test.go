package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
}

func writeAuth(t *testing.T, path string, access string) {
	t.Helper()
	doc := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"id_token":      jwt(map[string]any{"email": "a@x", "https://api.openai.com/auth": map[string]string{"chatgpt_plan_type": "plus"}}),
			"access_token":  access,
			"refresh_token": "r1",
			"account_id":    "acc-1",
		},
		"keep": "me",
	}
	b, _ := json.Marshal(doc)
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityAndCopy(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	os.Setenv("CODEX_HOME", "")

	live := LivePath()
	writeAuth(t, live, "x")
	id, err := ReadIdentity(live)
	if err != nil || id != (Identity{Email: "a@x", Plan: "plus", AccountID: "acc-1"}) {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	saved := filepath.Join(h, "saved", "auth.json")
	if err := Copy(live, saved); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(saved); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("saved mode = %v", fi.Mode().Perm())
	}
}

func TestUsageRefreshesSavedLoginAndClassifiesWindows(t *testing.T) {
	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshed = true
			w.Write([]byte(`{"access_token":"` + jwt(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}) + `","refresh_token":"r2"}`))
		case "/usage":
			if r.Header.Get("chatgpt-account-id") != "acc-1" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				w.WriteHeader(401)
				return
			}
			// Weekly-only answers arrive as the primary window.
			w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_at":1900000000},"secondary_window":null}}`))
		}
	}))
	defer srv.Close()
	tokenURL, usageURL = srv.URL+"/token", srv.URL+"/usage"

	p := filepath.Join(t.TempDir(), "auth.json")
	expired := jwt(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	writeAuth(t, p, expired)

	if _, err := Usage(context.Background(), p, false); err == nil {
		t.Fatal("live login must not be refreshed")
	}
	q, err := Usage(context.Background(), p, true)
	if err != nil || !refreshed {
		t.Fatalf("usage: %v refreshed=%v", err, refreshed)
	}
	if q.Weekly.Percent != 40 || q.Session.Percent != 0 || q.Weekly.ResetsAt.Unix() != 1900000000 {
		t.Fatalf("quota = %+v", q)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"refresh_token": "r2"`) || !strings.Contains(string(b), `"keep": "me"`) {
		t.Fatalf("rotated tokens not saved, or other keys lost:\n%s", b)
	}
}

func TestKeyringLogin(t *testing.T) {
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("CODEX_HOME", "")
	items := map[string]string{}
	oldR, oldW := kcRead, kcWrite
	t.Cleanup(func() { kcRead, kcWrite = oldR, oldW })
	kcRead = func(s, a string) (string, bool, error) { v, ok := items[s+"/"+a]; return v, ok, nil }
	kcWrite = func(s, a, d string) error { items[s+"/"+a] = d; return nil }

	os.MkdirAll(harness.CodexHome(), 0o700)
	os.WriteFile(filepath.Join(harness.CodexHome(), "config.toml"), []byte("cli_auth_credentials_store = \"keyring\" # mine\n"), 0o600)
	saved := filepath.Join(h, "saved.json")
	writeAuth(t, saved, "x")
	writeAuth(t, LivePath(), "stale")
	if err := UseLogin(saved); err != nil {
		t.Fatal(err)
	}
	key := keyringService + "/" + keyringKey(harness.CodexHome())
	if !strings.HasPrefix(key, "Codex Auth/cli|") || len(key) != len("Codex Auth/cli|")+16 || !strings.Contains(items[key], `"account_id":"acc-1"`) {
		t.Fatalf("keychain items = %v", items)
	}
	if _, err := os.Stat(LivePath()); !os.IsNotExist(err) {
		t.Fatal("stale auth.json left beside the keychain login")
	}
	p, cleanup, err := LiveFile()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if id, err := ReadIdentity(p); err != nil || id.Email != "a@x" {
		t.Fatalf("live identity = %+v, %v", id, err)
	}
}
