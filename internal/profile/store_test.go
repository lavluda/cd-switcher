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
