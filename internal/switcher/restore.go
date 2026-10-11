package switcher

import "time"

const (
	// updateQuitSlack absorbs the one-second resolution of Claude's log
	// timestamps when comparing them with when a window was last seen.
	updateQuitSlack = 2 * time.Second
	// restoreDelay gives Claude's updater time to finish replacing the app
	// before a window is relaunched.
	restoreDelay = 8 * time.Second
)

// Restorer decides which side-by-side windows to reopen after Claude quit them
// to install an update. It only acts on windows it saw running, and only when
// Claude's log says an update restart happened after that — a window the user
// closed on purpose stays closed.
type Restorer struct {
	seen    map[string]time.Time // id -> last time the window was running
	pending map[string]time.Time // id -> when its update quit was noticed
}

func NewRestorer() *Restorer {
	return &Restorer{seen: map[string]time.Time{}, pending: map[string]time.Time{}}
}

// Observe records which of ids are running at now. updateQuit is the time of
// the latest update restart in Claude's log (zero if none).
func (r *Restorer) Observe(now time.Time, ids []string, running map[string]bool, updateQuit time.Time) {
	for _, id := range ids {
		if running[id] {
			r.seen[id] = now
			delete(r.pending, id)
			continue
		}
		last, wasSeen := r.seen[id]
		if !wasSeen {
			continue
		}
		delete(r.seen, id)
		if !updateQuit.IsZero() && !updateQuit.Before(last.Add(-updateQuitSlack)) {
			r.pending[id] = now
		}
	}
}

// Due returns the windows whose update restart was noticed long enough ago to
// reopen them now.
func (r *Restorer) Due(now time.Time) []string {
	var ids []string
	for id, since := range r.pending {
		if now.Sub(since) >= restoreDelay {
			ids = append(ids, id)
		}
	}
	return ids
}

// Done marks ids as handed off for reopening.
func (r *Restorer) Done(ids []string) {
	for _, id := range ids {
		delete(r.pending, id)
	}
}
