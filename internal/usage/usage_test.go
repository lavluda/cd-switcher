package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"five_hour": {"utilization": 42, "resets_at": "2026-07-14T15:00:00Z"},
			"seven_day": {"utilization": 10, "resets_at": "2026-07-20T00:00:00Z"},
			"extra_usage": {"monthly_limit": 100, "used_credits": 5, "utilization": 5, "currency": "USD"},
			"limits": [{"kind": "five_hour", "percent": 42, "severity": "warning", "resets_at": "2026-07-14T15:00:00Z", "is_active": true}]
		}`))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL, http.DefaultClient)
	stats, err := c.Fetch(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// utilization is already a 0-100 percentage, confirmed against the real
	// endpoint — not a 0-1 fraction.
	if stats.FiveHour.Utilization != 42 {
		t.Errorf("FiveHour.Utilization = %v, want 42", stats.FiveHour.Utilization)
	}
	wantReset := time.Date(2026, 7, 14, 15, 0, 0, 0, time.UTC)
	if !stats.FiveHour.ResetsAt.Equal(wantReset) {
		t.Errorf("FiveHour.ResetsAt = %v, want %v", stats.FiveHour.ResetsAt, wantReset)
	}
	if len(stats.Limits) != 1 || stats.Limits[0].Kind != "five_hour" {
		t.Errorf("Limits = %+v", stats.Limits)
	}
}

func TestFetchNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL, http.DefaultClient)
	_, err := c.Fetch(context.Background(), "bad-token")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestFetchMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL, http.DefaultClient)
	_, err := c.Fetch(context.Background(), "token")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}
