package updatecheck

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.3", "v0.1.4", true},
		{"v0.1.3", "v0.1.3", false},
		{"v0.1.4", "v0.1.3", false},
		{"v0.9.9", "v1.0.0", true},
		{"v1.2.0", "v1.10.0", true}, // numeric, not lexical, comparison
		{"0.1.3", "0.1.4", true},    // tolerates a missing leading "v"
		{"v0.1", "v0.1.1", true},    // short tag, missing patch defaults to 0
		{"dev", "v0.1.4", false},    // unparseable current: never claims outdated
		{"v0.1.3", "not-a-version", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
