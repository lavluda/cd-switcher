package usage

import "testing"

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
