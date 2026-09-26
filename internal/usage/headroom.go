package usage

// AccountLoad is one profile's latest session and weekly utilization, used to
// choose which account still has room.
type AccountLoad struct {
	ID      string
	Session float64
	Week    float64
	Ready   bool
}

// Freest returns the ready account with the lowest session utilization.
// A tie goes to the lower weekly utilization, then to the earlier entry.
func Freest(loads []AccountLoad) (AccountLoad, bool) {
	var best AccountLoad
	found := false
	for _, load := range loads {
		if !load.Ready {
			continue
		}
		if !found || load.Session < best.Session || (load.Session == best.Session && load.Week < best.Week) {
			best = load
			found = true
		}
	}
	return best, found
}
