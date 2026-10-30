package usage

import (
	"testing"
	"time"
)

func TestFreestPrefersLowerSessionThenWeek(t *testing.T) {
	loads := []AccountLoad{
		{ID: "busy", Session: 80, Week: 10, Ready: true},
		{ID: "idle", Session: 12, Week: 40, Ready: true},
		{ID: "unknown", Session: 0, Ready: false},
		{ID: "also-idle", Session: 12, Week: 90, Ready: true},
	}
	got, ok := Freest(loads)
	if !ok || got.ID != "idle" {
		t.Fatalf("Freest = %+v, %v; want idle", got, ok)
	}
}

func TestFreestNoneReady(t *testing.T) {
	if _, ok := Freest([]AccountLoad{{ID: "a"}}); ok {
		t.Fatal("expected no choice without a ready reading")
	}
}

func TestFreestFavoursSoonResettingSession(t *testing.T) {
	loads := []AccountLoad{
		{ID: "steady", Session: 20, Week: 10, ResetsIn: 4 * time.Hour, Ready: true},
		{ID: "resetting", Session: 60, Week: 10, ResetsIn: 30 * time.Minute, Ready: true},
	}
	if got, _ := Freest(loads); got.ID != "resetting" {
		t.Fatalf("Freest = %q; want resetting", got.ID)
	}
}

func TestFreestWeeklyCanBeTheBindingLimit(t *testing.T) {
	loads := []AccountLoad{
		{ID: "weekly-full", Session: 0, Week: 95, Ready: true},
		{ID: "busy-session", Session: 30, Week: 10, Ready: true},
	}
	if got, _ := Freest(loads); got.ID != "busy-session" {
		t.Fatalf("Freest = %q; want busy-session", got.ID)
	}
}

func TestConsumeFirstSpendsSoonestWeeklyReset(t *testing.T) {
	loads := []AccountLoad{
		{ID: "busy", Session: 99, WeekResetsIn: time.Hour, Ready: true}, // over the cap
		{ID: "late", Session: 10, WeekResetsIn: 72 * time.Hour, Ready: true},
		{ID: "soon", Session: 50, Week: 60, WeekResetsIn: 6 * time.Hour, Ready: true},
		{ID: "unknown", Session: 0, Ready: true},
		{ID: "excluded", Session: 0, WeekResetsIn: time.Minute, Ready: false},
	}
	if got, ok := ConsumeFirst(loads, 90); !ok || got.ID != "soon" {
		t.Fatalf("got %q", got.ID)
	}
	if got, ok := ConsumeFirst(loads[3:], 90); !ok || got.ID != "unknown" {
		t.Fatalf("unknown reset should still be chosen when alone, got %q", got.ID)
	}
}

func TestAccountFromCacheKey(t *testing.T) {
	a, o := AccountFromCacheKey("11111111-2222-4333-8444-555555555555:66666666-7777-4888-9999-000000000000:https://api.anthropic.com:user:inference")
	if a != "11111111-2222-4333-8444-555555555555" || o != "66666666-7777-4888-9999-000000000000" {
		t.Fatalf("got %q %q", a, o)
	}
	if a, o := AccountFromCacheKey("https://api.anthropic.com:user:inference"); a != "" || o != "" {
		t.Fatal("parsed a key without UUIDs")
	}
}
