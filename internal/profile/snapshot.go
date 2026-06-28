package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// denylist names top-level entries in Claude Desktop's data dir that are pure
// caches / telemetry. They are never copied into or out of a snapshot — Claude
// regenerates them — which keeps snapshots small and avoids copying large,
// volatile cache trees.
var denylist = map[string]bool{
	"Cache":             true,
	"Code Cache":        true,
	"GPUCache":          true,
	"DawnGraphiteCache": true,
	"DawnWebGPUCache":   true,
	"blob_storage":      true,
	"Shared Dictionary": true,
	"Crashpad":          true,
	"sentry":            true,
	"pending-uploads":   true,
	"VideoDecodeStats":  true,
	// vm_bundles is Claude Desktop's local sandbox VM image (claudevm.bundle /
	// rootfs.img). It is regenerable runtime state, not account identity, but
	// runs to many gigabytes — copying it per profile bloats snapshots and can
	// exhaust the disk mid-switch. Claude rebuilds it on demand after a swap.
	"vm_bundles": true,
}

// Capture copies the live Claude data dir into the snapshot dir for a profile,
// replacing any previous snapshot. Cache/telemetry entries (see denylist) are
// skipped. The destination is built fresh so entries deleted in the live dir do
// not linger in the snapshot.
func Capture(liveDir, snapshotDir string) error {
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("clear snapshot: %w", err)
	}
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		return err
	}
	return copyTree(liveDir, snapshotDir)
}

// Restore copies a profile snapshot over the live Claude data dir. Live cache
// entries are left untouched; every non-cache live entry is removed first so
// the result exactly matches the snapshot, then the snapshot is copied in.
//
// On any error after mutation begins, the caller is expected to roll back from
// the backup created by BackupLive.
func Restore(snapshotDir, liveDir string) error {
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		return err
	}
	if err := clearNonCache(liveDir); err != nil {
		return fmt.Errorf("clear live: %w", err)
	}
	return copyTree(snapshotDir, liveDir)
}

// BackupLive moves the live dir's non-cache entries into backupDir so a failed
// Restore can be undone. backupDir must not already exist.
func BackupLive(liveDir, backupDir string) error {
	if err := os.RemoveAll(backupDir); err != nil {
		return err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if denylist[e.Name()] {
			continue
		}
		if err := os.Rename(filepath.Join(liveDir, e.Name()), filepath.Join(backupDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// RollbackLive restores a backup produced by BackupLive, undoing a partial
// Restore. It clears whatever the failed Restore wrote, then moves the backup
// back into place.
func RollbackLive(backupDir, liveDir string) error {
	if err := clearNonCache(liveDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(backupDir, e.Name()), filepath.Join(liveDir, e.Name())); err != nil {
			return err
		}
	}
	return os.RemoveAll(backupDir)
}

// Clear removes every non-cache entry from the live dir, leaving Claude in a
// logged-out state (it recreates what it needs on next launch).
func Clear(liveDir string) error {
	return clearNonCache(liveDir)
}

// clearNonCache removes every top-level entry in dir except denylisted caches.
func clearNonCache(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if denylist[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies the non-denylisted top-level entries of src into dst,
// recursing into directories. Symlinks are skipped (Claude's data dir contains
// none of interest, and copying them as files would be wrong).
func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if denylist[e.Name()] {
			continue
		}
		if err := copyEntry(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return nil // skip symlinks
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		children, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, c := range children {
			if err := copyEntry(filepath.Join(src, c.Name()), filepath.Join(dst, c.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		return copyFile(src, dst, info)
	default:
		return nil // sockets/devices: nothing meaningful to copy
	}
}

func copyFile(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
