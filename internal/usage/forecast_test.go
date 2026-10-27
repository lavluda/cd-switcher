package usage

import (
	"testing"
	"time"
)

func TestForecastSession(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	rising := []Sample{{Time: t0, FiveHour: 20}, {Time: t0.Add(30 * time.Minute), FiveHour: 40}}
	now := t0.Add(30 * time.Minute)

	// 20 points per 30 minutes: the remaining 60 points take 90 minutes.
	eta, ok := ForecastSession(rising, now, now.Add(4*time.Hour))
	if !ok || !eta.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("eta = %v, %v; want %v", eta, ok, now.Add(90*time.Minute))
	}
	if _, ok := ForecastSession(rising, now, now.Add(time.Hour)); ok {
		t.Error("forecast past the reset time should be dropped")
	}
	flat := []Sample{{Time: t0, FiveHour: 40}, {Time: t0.Add(30 * time.Minute), FiveHour: 40}}
	if _, ok := ForecastSession(flat, now, time.Time{}); ok {
		t.Error("flat usage should not forecast")
	}
	// A drop in the middle is a reset: only the samples after it count.
	reset := []Sample{{Time: t0, FiveHour: 90}, {Time: t0.Add(10 * time.Minute), FiveHour: 2}, {Time: t0.Add(30 * time.Minute), FiveHour: 12}}
	if eta, ok := ForecastSession(reset, now, time.Time{}); !ok || !eta.Equal(t0.Add(30*time.Minute).Add(176*time.Minute)) {
		t.Errorf("after reset: eta = %v, %v", eta, ok)
	}
	if _, ok := ForecastSession(rising[:1], now, time.Time{}); ok {
		t.Error("one sample should not forecast")
	}
}
