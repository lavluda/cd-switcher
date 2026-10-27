//go:build darwin

package clicreds

import (
	"strings"
	"testing"
)

// Round-trips through a throwaway keychain service, never the real CLI item.
func TestWriteReadRemoveRoundTrip(t *testing.T) {
	const svc = "cd-switcher-test-roundtrip"
	t.Cleanup(func() { _ = remove(svc) })
	for _, n := range []int{50, 1500, 6000} { // the last exceeds `security -i`'s line limit
		data := `{"k":"` + strings.Repeat("x", n) + `"}`
		if err := write(svc, data); err != nil {
			t.Fatalf("write %d bytes: %v", n, err)
		}
		got, found, err := read(svc)
		if err != nil || !found || got != data {
			t.Fatalf("read back %d bytes: found=%v err=%v intact=%v", n, found, err, got == data)
		}
	}
	if err := remove(svc); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := read(svc); found {
		t.Fatal("item still present after remove")
	}
}
