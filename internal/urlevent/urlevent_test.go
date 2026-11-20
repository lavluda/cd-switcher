//go:build !darwin

package urlevent

import (
	"testing"
	"time"
)

func TestForwardReachesListener(t *testing.T) {
	dir := t.TempDir()
	if Forward(dir, "cdswitcher://v1/x") {
		t.Fatal("forwarded with nothing listening")
	}
	links := Listen(dir)
	if !Forward(dir, "cdswitcher://v1/x\n") {
		t.Fatal("forward failed")
	}
	select {
	case got := <-links:
		if got != "cdswitcher://v1/x" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("link never arrived")
	}
}
