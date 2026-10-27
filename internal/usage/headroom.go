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
	Ready    bool
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
