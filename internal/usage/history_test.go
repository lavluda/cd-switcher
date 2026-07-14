package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadHistoryMissingFile(t *testing.T) {
	samples, err := LoadHistory(filepath.Join(t.TempDir(), "usage_history.json"))
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if samples != nil {
		t.Fatalf("samples = %v, want nil", samples)
	}
}

func TestAppendHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage_history.json")
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	if err := AppendHistory(path, Sample{Time: now, FiveHour: 10, SevenDay: 5}); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	if err := AppendHistory(path, Sample{Time: now.Add(5 * time.Minute), FiveHour: 20, SevenDay: 6}); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}

	samples, err := LoadHistory(path)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if samples[0].FiveHour != 10 || samples[1].FiveHour != 20 {
		t.Fatalf("samples = %+v", samples)
	}
	if !samples[1].Time.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("samples[1].Time = %v", samples[1].Time)
	}
}

func TestAppendHistoryTrimsToCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage_history.json")
	base := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

	for i := 0; i < MaxHistorySamples+10; i++ {
		s := Sample{Time: base.Add(time.Duration(i) * time.Minute), FiveHour: float64(i)}
		if err := AppendHistory(path, s); err != nil {
			t.Fatalf("AppendHistory(%d): %v", i, err)
		}
	}

	samples, err := LoadHistory(path)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(samples) != MaxHistorySamples {
		t.Fatalf("len(samples) = %d, want %d", len(samples), MaxHistorySamples)
	}
	// Oldest 10 should have been trimmed away; the first remaining sample is
	// the 11th one appended (FiveHour == 10).
	if samples[0].FiveHour != 10 {
		t.Fatalf("samples[0].FiveHour = %v, want 10 (oldest should be trimmed)", samples[0].FiveHour)
	}
	if samples[len(samples)-1].FiveHour != float64(MaxHistorySamples+9) {
		t.Fatalf("samples[last].FiveHour = %v, want %d", samples[len(samples)-1].FiveHour, MaxHistorySamples+9)
	}
}
