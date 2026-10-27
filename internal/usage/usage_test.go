package usage

import (
	"context"
	"encoding/json"
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

func TestFableMeterFromEitherShape(t *testing.T) {
	for _, body := range []string{
		`{"seven_day_fable":{"utilization":12,"resets_at":"2026-10-30T00:00:00Z"}}`,
		`{"limits":[{"kind":"weekly_scoped","percent":12,"resets_at":"2026-10-30T00:00:00Z","scope":{"model":{"display_name":"Fable"}}}]}`,
	} {
		var s Stats
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		var got *Meter
		for _, m := range s.Meters() {
			if m.Label == "Fable" {
				got = &m
			}
		}
		if got == nil || got.Utilization != 12 {
			t.Errorf("no Fable meter from %s: %+v", body, s.Meters())
		}
	}
}

func TestFetchRicherOrgUsageAndTokens(t *testing.T) {
	const org = "11111111-2222-4333-8444-555555555555"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/oauth/usage":
			w.Write([]byte(`{"five_hour":{"utilization":1,"resets_at":"2026-07-14T15:00:00Z"}}`))
		case "/api/oauth/profile":
			w.Write([]byte(`{"organization":{"uuid":"` + org + `"}}`))
		case "/api/organizations/" + org + "/usage":
			w.Write([]byte(`{
				"five_hour": {"utilization": 40, "resets_at": 1784041200, "input_tokens": 1200, "output_tokens": 300},
				"seven_day": {"utilization": null, "resets_at": "2026-07-20T00:00:00Z"},
				"seven_day_opus": {"utilization": 8, "resets_at": "2026-07-20T00:00:00Z"},
				"extra_usage": {"is_enabled": true, "monthly_limit": 2000, "used_credits": 150, "utilization": 7.5, "currency": "USD"},
				"input_tokens": 50000,
				"output_tokens": 8000,
				"limits": [{"kind": "weekly_scoped", "percent": 15, "resets_at": "2026-07-20T00:00:00Z", "scope": {"model": {"display_name": "Haiku"}}}]
			}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newClientWithBase(srv.URL, http.DefaultClient)
	stats, err := c.Fetch(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if stats.FiveHour.Utilization != 40 {
		t.Errorf("FiveHour = %v, want org payload 40", stats.FiveHour.Utilization)
	}
	if stats.FiveHour.CountLabel() != "1.2k in · 300 out" {
		t.Errorf("CountLabel = %q", stats.FiveHour.CountLabel())
	}
	if stats.SevenDayOpus.Utilization != 8 {
		t.Errorf("Opus = %v", stats.SevenDayOpus.Utilization)
	}
	if stats.Tokens.Input != 50000 || stats.Tokens.Output != 8000 {
		t.Errorf("Tokens = %+v", stats.Tokens)
	}
	if stats.ExtraUsage.SpendLabel() != "Extra $1.50 of $20.00" {
		t.Errorf("SpendLabel = %q", stats.ExtraUsage.SpendLabel())
	}
	labels := map[string]bool{}
	for _, m := range stats.Meters() {
		labels[m.Label] = true
	}
	for _, want := range []string{"Session", "This week", "Opus", "Haiku"} {
		if !labels[want] {
			t.Errorf("meters missing %q: %+v", want, stats.Meters())
		}
	}
	wantReset := time.Unix(1784041200, 0).UTC()
	if !stats.FiveHour.ResetsAt.Equal(wantReset) {
		t.Errorf("numeric resets_at = %v, want %v", stats.FiveHour.ResetsAt, wantReset)
	}

	// The organization id is cached, so a second fetch skips the profile call
	// and still returns the richer payload.
	again, err := c.Fetch(context.Background(), "test-token")
	if err != nil || again.Tokens.Total() != stats.Tokens.Total() {
		t.Fatalf("cached fetch = %+v, %v", again.Tokens, err)
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
