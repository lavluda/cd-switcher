package profile

import (
	"fmt"
	"os"
	"path/filepath"
)

// shared names are machine configuration, not account identity. They stay in
// the live Claude data directory across switches, and side-by-side windows
// see them through a symlink back to that directory.
var shared = map[string]bool{
	"claude_desktop_config.json":    true,
	"Claude Extensions":             true,
	"Claude Extensions Settings":    true,
	"extensions-installations.json": true,
	"extensions-blocklist.json":     true,
	"mcp-user-tool-toggles.json":    true,
}

func skipEntry(name string) bool {
	return denylist[name] || shared[name]
}

// LinkShared points each shared entry in profileDir at the canonical copy in
// liveDir, so every side-by-side window edits the same MCP and extension
// config. Missing canonical entries are skipped. An existing real file or
// directory in the profile is replaced by the link.
func LinkShared(liveDir, profileDir string) error {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return err
	}
	for name := range shared {
		src := filepath.Join(liveDir, name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(profileDir, name)
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("replace %s: %w", name, err)
		}
		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("link %s: %w", name, err)
		}
	}
	return nil
}
