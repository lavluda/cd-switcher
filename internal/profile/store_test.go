package profile

import (
	"path/filepath"
	"testing"
)

func TestConfigRename(t *testing.T) {
	c := &Config{Profiles: []Profile{{ID: "work", Label: "Work"}}}

	if !c.Rename("work", "Work 2") {
		t.Fatal("Rename returned false for existing id")
	}
	if got := c.Profiles[0].Label; got != "Work 2" {
		t.Fatalf("label = %q, want %q", got, "Work 2")
	}
	if c.Profiles[0].ID != "work" {
		t.Fatalf("id changed to %q, want it stable", c.Profiles[0].ID)
	}
	if c.Rename("nope", "x") {
		t.Fatal("Rename returned true for unknown id")
	}
}

func TestConfigRemove(t *testing.T) {
	c := &Config{
		ActiveProfile: "work",
		Profiles:      []Profile{{ID: "personal"}, {ID: "work"}},
	}

	// Removing the active profile clears ActiveProfile.
	if !c.Remove("work") {
		t.Fatal("Remove returned false for existing id")
	}
	if c.ActiveProfile != "" {
		t.Fatalf("ActiveProfile = %q, want cleared after removing active", c.ActiveProfile)
	}
	if _, ok := c.Find("work"); ok {
		t.Fatal("work still present after Remove")
	}
	if len(c.Profiles) != 1 || c.Profiles[0].ID != "personal" {
		t.Fatalf("remaining profiles = %+v, want [personal]", c.Profiles)
	}

	// Removing a non-active profile leaves ActiveProfile alone.
	c.ActiveProfile = "personal"
	c.Profiles = append(c.Profiles, Profile{ID: "extra"})
	if !c.Remove("extra") {
		t.Fatal("Remove returned false for existing id")
	}
	if c.ActiveProfile != "personal" {
		t.Fatalf("ActiveProfile = %q, want personal untouched", c.ActiveProfile)
	}
	if c.Remove("ghost") {
		t.Fatal("Remove returned true for unknown id")
	}
}

func TestWarnPercent(t *testing.T) {
	if got := (&Config{}).WarnPercent(); got != DefaultWarnPercent {
		t.Fatalf("default = %d, want %d", got, DefaultWarnPercent)
	}
	off := 0
	if got := (&Config{WarnAt: &off}).WarnPercent(); got != 0 {
		t.Fatalf("off = %d, want 0", got)
	}
	n := 70
	if got := (&Config{WarnAt: &n}).WarnPercent(); got != 70 {
		t.Fatalf("set = %d, want 70", got)
	}
}

func TestAutoCheckUpdatesEnabled(t *testing.T) {
	if !(&Config{}).AutoCheckUpdatesEnabled() {
		t.Fatal("default should be enabled")
	}
	off := false
	if (&Config{AutoCheckUpdates: &off}).AutoCheckUpdatesEnabled() {
		t.Fatal("explicit false should be disabled")
	}
	on := true
	if !(&Config{AutoCheckUpdates: &on}).AutoCheckUpdatesEnabled() {
		t.Fatal("explicit true should be enabled")
	}
}

func TestConfigMove(t *testing.T) {
	c := &Config{Profiles: []Profile{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	if !c.Move("b", -1) {
		t.Fatal("Move up failed")
	}
	if c.Profiles[0].ID != "b" || c.Profiles[1].ID != "a" {
		t.Fatalf("order = %+v", c.Profiles)
	}
	if c.Move("b", -1) {
		t.Fatal("Move past the start should fail")
	}
	if !c.Move("a", 1) {
		t.Fatal("Move down failed")
	}
	if c.Profiles[2].ID != "a" {
		t.Fatalf("order = %+v", c.Profiles)
	}
}

func TestStoreRemoveSnapshot(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := store.ProfileDir("work")
	writeFile(t, filepath.Join(dir, "Cookies"), "x")

	if !exists(dir) {
		t.Fatal("snapshot dir not created by test setup")
	}
	if err := store.RemoveSnapshot("work"); err != nil {
		t.Fatalf("RemoveSnapshot: %v", err)
	}
	if exists(dir) {
		t.Fatal("snapshot dir still present after RemoveSnapshot")
	}
	// Removing a non-existent snapshot is a no-op, not an error.
	if err := store.RemoveSnapshot("missing"); err != nil {
		t.Fatalf("RemoveSnapshot(missing): %v", err)
	}
}
