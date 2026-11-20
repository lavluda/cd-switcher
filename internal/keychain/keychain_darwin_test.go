//go:build darwin

package keychain

import (
	"strings"
	"testing"
)

// Round-trips through a throwaway keychain service, never the real CLI item.
func TestWriteReadRemoveRoundTrip(t *testing.T) {
	const svc = "cd-switcher-test-roundtrip"
	t.Cleanup(func() { _ = Remove(svc, "") })
	for _, n := range []int{50, 1500, 6000} { // the last exceeds `security -i`'s line limit
		data := `{"k":"` + strings.Repeat("x", n) + `"}`
		if err := Write(svc, "cd-switcher-test", data); err != nil {
			t.Fatalf("write %d bytes: %v", n, err)
		}
		got, found, err := Read(svc, "cd-switcher-test")
		if err != nil || !found || got != data {
			t.Fatalf("read back %d bytes: found=%v err=%v intact=%v", n, found, err, got == data)
		}
	}
	if err := Remove(svc, ""); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Read(svc, ""); found {
		t.Fatal("item still present after remove")
	}
}
