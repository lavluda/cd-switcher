package usage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestRefresh(t *testing.T) {
	var gotBody string
	c := newClientWithBase("https://api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/oauth/token" {
			t.Errorf("path = %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		body := `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}))
	got, err := c.Refresh(context.Background(), "old-refresh", "user:inference")
	if err != nil {
		t.Fatal(err)
	}
	if got.Access != "new-access" || got.Refresh != "new-refresh" {
		t.Fatalf("got %+v", got)
	}
	if time.Until(got.ExpiresAt) < 50*time.Minute {
		t.Fatalf("expires at %s, want about an hour out", got.ExpiresAt)
	}
	if !strings.Contains(gotBody, `"refresh_token":"old-refresh"`) {
		t.Fatalf("body = %s", gotBody)
	}
	if !strings.Contains(gotBody, desktopClientID) {
		t.Fatalf("body missing client id: %s", gotBody)
	}
}

func TestScopeFromCacheKey(t *testing.T) {
	got := ScopeFromCacheKey("acct:org:https://api.anthropic.com:user:inference")
	if got != "user:inference" {
		t.Fatalf("scope = %q", got)
	}
	if ScopeFromCacheKey("no-url") != defaultScope {
		t.Fatal("expected default scope")
	}
}
