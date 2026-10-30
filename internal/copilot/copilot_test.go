package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func TestFetchSkipsRevokedTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token good" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"login":"me","copilot_plan":"individual","quota_snapshots":{"premium_interactions":{"percent_remaining":75.5,"remaining":226,"entitlement":300,"unlimited":false,"quota_reset_at":1900000000}}}`))
	}))
	defer srv.Close()
	userURL = srv.URL

	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(h, ".config", "github-copilot")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "apps.json"), []byte(`{"github.com:a":{"oauth_token":"revoked"},"github.com:b":{"oauth_token":"good"}}`), 0o600)

	q, err := Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if q.Login != "me" || q.Percent != 24.5 || q.Total != 300 || q.ResetsAt.Unix() != 1900000000 {
		t.Fatalf("quota = %+v", q)
	}
}
