package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file with content, making parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// makeLive builds a fake Claude data dir with identity files, a nested dir, and
// a cache entry that must be ignored by snapshotting.
func makeLive(t *testing.T) string {
	t.Helper()
	live := t.TempDir()
	writeFile(t, filepath.Join(live, "Cookies"), "session=A")
	writeFile(t, filepath.Join(live, "config.json"), `{"oauth":"A"}`)
	writeFile(t, filepath.Join(live, "Local Storage", "leveldb", "000.ldb"), "ldbA")
	writeFile(t, filepath.Join(live, "Cache", "big.bin"), "cacheA")                             // denylisted
	writeFile(t, filepath.Join(live, "vm_bundles", "claudevm.bundle", "rootfs.img"), "vmimage") // denylisted
	writeFile(t, filepath.Join(live, "claude_desktop_config.json"), `{"mcp":"shared"}`)         // shared
	return live
}

func TestCaptureSkipsCacheAndCopiesNested(t *testing.T) {
	live := makeLive(t)
	snap := filepath.Join(t.TempDir(), "snap")

	if err := Capture(live, snap); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(snap, "Cookies")); got != "session=A" {
		t.Errorf("Cookies = %q", got)
	}
	if got := readFile(t, filepath.Join(snap, "Local Storage", "leveldb", "000.ldb")); got != "ldbA" {
		t.Errorf("nested ldb = %q", got)
	}
	if exists(filepath.Join(snap, "Cache")) {
		t.Error("Cache should not be captured")
	}
	if exists(filepath.Join(snap, "vm_bundles")) {
		t.Error("vm_bundles (multi-GB VM image) should not be captured")
	}
	if exists(filepath.Join(snap, "claude_desktop_config.json")) {
		t.Error("MCP config should stay out of the snapshot")
	}
}

func TestCaptureKeepsOldSnapshotWhenCopyFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-0 files")
	}
	dir := t.TempDir()
	live, snap := filepath.Join(dir, "live"), filepath.Join(dir, "snap")
	writeFile(t, filepath.Join(snap, "config.json"), "old")
	writeFile(t, filepath.Join(live, "config.json"), "new")
	if err := os.Chmod(filepath.Join(live, "config.json"), 0); err != nil {
		t.Fatal(err)
	}
	if err := Capture(live, snap); err == nil {
		t.Fatal("expected copy of an unreadable file to fail")
	}
	if got := readFile(t, filepath.Join(snap, "config.json")); got != "old" {
		t.Fatalf("old snapshot lost: got %q", got)
	}
	if exists(snap + ".tmp") {
		t.Fatal("temp dir left behind")
	}
}

func TestRestoreReplacesIdentityKeepsCache(t *testing.T) {
	// Snapshot of account B.
	snapB := filepath.Join(t.TempDir(), "B")
	writeFile(t, filepath.Join(snapB, "Cookies"), "session=B")
	writeFile(t, filepath.Join(snapB, "config.json"), `{"oauth":"B"}`)

	// Live currently holds account A plus a cache the restore must preserve.
	live := makeLive(t)

	if err := Restore(snapB, live); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(live, "Cookies")); got != "session=B" {
		t.Errorf("Cookies after restore = %q, want session=B", got)
	}
	// Account A's leveldb (not in B) must be gone.
	if exists(filepath.Join(live, "Local Storage")) {
		t.Error("stale identity dir should be cleared on restore")
	}
	// Cache is denylisted and must survive untouched.
	if got := readFile(t, filepath.Join(live, "Cache", "big.bin")); got != "cacheA" {
		t.Errorf("cache should be preserved, got %q", got)
	}
	if got := readFile(t, filepath.Join(live, "claude_desktop_config.json")); got != `{"mcp":"shared"}` {
		t.Errorf("MCP config should survive a restore, got %q", got)
	}
}

func TestBackupAndRollbackRoundTrips(t *testing.T) {
	live := makeLive(t)
	bak := filepath.Join(t.TempDir(), "bak")

	if err := BackupLive(live, bak); err != nil {
		t.Fatal(err)
	}
	// After backup, identity moved out; cache stays.
	if exists(filepath.Join(live, "Cookies")) {
		t.Error("Cookies should have moved to backup")
	}
	if !exists(filepath.Join(live, "Cache", "big.bin")) {
		t.Error("cache should remain during backup")
	}
	if !exists(filepath.Join(live, "claude_desktop_config.json")) {
		t.Error("MCP config should stay in the live dir during backup")
	}

	// Simulate a half-written restore, then roll back.
	writeFile(t, filepath.Join(live, "Cookies"), "session=PARTIAL")
	if err := RollbackLive(bak, live); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(live, "Cookies")); got != "session=A" {
		t.Errorf("rollback Cookies = %q, want session=A", got)
	}
	if got := readFile(t, filepath.Join(live, "Local Storage", "leveldb", "000.ldb")); got != "ldbA" {
		t.Errorf("rollback nested = %q", got)
	}
	if exists(bak) {
		t.Error("backup dir should be removed after rollback")
	}
}

func TestClearLeavesCache(t *testing.T) {
	live := makeLive(t)
	if err := Clear(live); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(live, "Cookies")) {
		t.Error("Clear should remove identity files")
	}
	if !exists(filepath.Join(live, "Cache", "big.bin")) {
		t.Error("Clear should keep cache")
	}
	if !exists(filepath.Join(live, "claude_desktop_config.json")) {
		t.Error("Clear should keep shared MCP config")
	}
}

func TestLinkSharedReplacesProfileCopy(t *testing.T) {
	live := t.TempDir()
	profileDir := t.TempDir()
	writeFile(t, filepath.Join(live, "claude_desktop_config.json"), `{"mcp":"live"}`)
	writeFile(t, filepath.Join(profileDir, "claude_desktop_config.json"), `{"mcp":"old"}`)

	if err := LinkShared(live, profileDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(profileDir, "claude_desktop_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"mcp":"live"}` {
		t.Fatalf("linked config = %q", got)
	}
	info, err := os.Lstat(filepath.Join(profileDir, "claude_desktop_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("shared config should be a symlink")
	}
}

func TestNewIDUnique(t *testing.T) {
	cfg := &Config{Profiles: []Profile{{ID: "work"}, {ID: "work-2"}}}
	if got := cfg.NewID("Work"); got != "work-3" {
		t.Errorf("NewID = %q, want work-3", got)
	}
	if got := cfg.NewID("Personal Account!"); got != "personal-account" {
		t.Errorf("NewID slug = %q", got)
	}
}
