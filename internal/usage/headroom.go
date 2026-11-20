package usage

import (
	"math"
	"time"
)

// SessionWindow is the length of the rolling session limit.
const SessionWindow = 5 * time.Hour

// AccountLoad is one profile's latest session and weekly utilization, used to
// choose which account still has room. ResetsIn is how long until the session
// window resets; zero means unknown.
type AccountLoad struct {
	ID       string
	Session  float64
	Week     float64
	ResetsIn time.Duration
	// WeekResetsIn is how long until the weekly window resets; zero means
	// unknown.
	WeekResetsIn time.Duration
	Ready        bool
}

// score is how constrained the account is: the weekly figure, or the session
// figure scaled down by how soon it resets (a session about to reset is nearly
// free), whichever is worse. Lower is better.
func (l AccountLoad) score() float64 {
	s := l.Session
	if l.ResetsIn > 0 && l.ResetsIn < SessionWindow {
		s *= float64(l.ResetsIn) / float64(SessionWindow)
	}
	return math.Max(s, l.Week)
}

// Freest returns the ready account with the lowest score. A tie goes to the
// lower session utilization, then to the earlier entry.
func Freest(loads []AccountLoad) (AccountLoad, bool) {
	var best AccountLoad
	found := false
	for _, load := range loads {
		if !load.Ready {
			continue
		}
		if !found || load.score() < best.score() || (load.score() == best.score() && load.Session < best.Session) {
			best = load
			found = true
		}
	}
	return best, found
}

// ConsumeFirst returns the ready account whose weekly window resets soonest
// among those whose session is under sessionCap, so allowance that would
// expire unused gets spent first. Accounts with no known weekly reset come
// last; ties fall back to Freest's order.
func ConsumeFirst(loads []AccountLoad, sessionCap float64) (AccountLoad, bool) {
	var best AccountLoad
	found := false
	for _, l := range loads {
		if !l.Ready || l.Session >= sessionCap || l.Week >= 100 {
			continue
		}
		if !found || sooner(l, best) {
			best, found = l, true
		}
	}
	return best, found
}

func sooner(a, b AccountLoad) bool {
	switch {
	case a.WeekResetsIn > 0 && b.WeekResetsIn <= 0:
		return true
	case a.WeekResetsIn <= 0 && b.WeekResetsIn > 0:
		return false
	case a.WeekResetsIn != b.WeekResetsIn:
		return a.WeekResetsIn < b.WeekResetsIn
	}
	return a.score() < b.score()
}

// Rolled returns s with every window whose reset time has passed counted as
// reset: 0% and the next reset one period later. A reading kept through failed
// fetches would otherwise show last period's numbers as current.
func (s Stats) Rolled(now time.Time) Stats {
	roll := func(w *Window, period time.Duration) {
		if w.ResetsAt.IsZero() || w.ResetsAt.After(now) {
			return
		}
		*w = Window{ResetsAt: w.ResetsAt}
		for !w.ResetsAt.After(now) {
			w.ResetsAt = w.ResetsAt.Add(period)
		}
	}
	const week = 7 * 24 * time.Hour
	roll(&s.FiveHour, 5*time.Hour)
	for _, w := range []*Window{&s.SevenDay, &s.SevenDaySonnet, &s.SevenDayOpus, &s.SevenDayFable,
		&s.SevenDayCowork, &s.SevenDayOAuthApps, &s.SevenDayOmelette, &s.OmelettePromotional} {
		roll(w, week)
	}
	limits := make([]Limit, len(s.Limits))
	for i, l := range s.Limits {
		w := Window{Utilization: l.Percent, ResetsAt: l.ResetsAt}
		if l.Kind == "five_hour" {
			roll(&w, 5*time.Hour)
		} else {
			roll(&w, week)
		}
		l.Percent, l.ResetsAt = w.Utilization, w.ResetsAt
		limits[i] = l
	}
	s.Limits = limits
	return s
}

// WithWindows returns s with the session and weekly windows replaced, for a
// reading that came from somewhere other than the usage API (Claude Code's
// statusline). A window that is not present keeps its old value.
func (s Stats) WithWindows(session float64, sessionReset time.Time, haveSession bool, week float64, weekReset time.Time, haveWeek bool) Stats {
	if haveSession {
		s.FiveHour.Utilization = session
		s.FiveHour.ResetsAt = sessionReset
	}
	if haveWeek {
		s.SevenDay.Utilization = week
		s.SevenDay.ResetsAt = weekReset
	}
	return s
}
