package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDailyRecordAndSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daily.json")
	now := time.Now()
	for _, r := range []struct {
		session, week float64
		reset         int64
	}{{40, 10, 1}, {100, 12, 1}, {100, 13, 1}, {20, 14, 2}, {100, 15, 3}} {
		if err := RecordDaily(path, now, r.session, r.week, r.reset); err != nil {
			t.Fatal(err)
		}
	}
	d, err := LoadDaily(path)
	if err != nil {
		t.Fatal(err)
	}
	got, days := d.Summary(now, 7)
	// Two distinct full windows (reset 1 and 3); the repeat reading of 1 is not a second hit.
	if days != 1 || got.Hits != 2 || got.PeakSession != 100 || got.PeakWeek != 15 {
		t.Fatalf("summary = %+v over %d days", got, days)
	}
}
