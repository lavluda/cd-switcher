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
