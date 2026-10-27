package usage

import "time"

// forecastLookback is how much recent history sets the burn rate, and
// forecastMinSpan the least it must cover before a forecast is trusted.
const (
	forecastLookback = time.Hour
	forecastMinSpan  = 10 * time.Minute
)

// ForecastSession estimates when the session limit reaches 100% by extending
// its recent burn rate. ok is false when usage isn't rising, history is too
// short, the limit is already full, or it would not fill before resetsAt.
func ForecastSession(samples []Sample, now, resetsAt time.Time) (time.Time, bool) {
	if len(samples) < 2 {
		return time.Time{}, false
	}
	last := samples[len(samples)-1]
	first := last
	// Walk back over the current window only: a drop means the window reset.
	for i := len(samples) - 2; i >= 0; i-- {
		s := samples[i]
		if s.FiveHour > samples[i+1].FiveHour || last.Time.Sub(s.Time) > forecastLookback {
			break
		}
		first = s
	}
	span := last.Time.Sub(first.Time)
	if span < forecastMinSpan || last.FiveHour >= 100 || last.FiveHour <= first.FiveHour {
		return time.Time{}, false
	}
	perSecond := (last.FiveHour - first.FiveHour) / span.Seconds()
	eta := last.Time.Add(time.Duration((100 - last.FiveHour) / perSecond * float64(time.Second)))
	if eta.Before(now) || (!resetsAt.IsZero() && eta.After(resetsAt)) {
		return time.Time{}, false
	}
	return eta, true
}
