//go:build linux

package claude

import "testing"

// TestIsClaudeDesktopProcessLinux guards against Quit() killing Claude Code:
// the native installer's binary is named "claude" but lives in a versions
// directory whose path never mentions "claude-code".
func TestIsClaudeDesktopProcessLinux(t *testing.T) {
	cases := []struct {
		name, exe string
		want      bool
	}{
		{"claude-desktop", "/usr/lib/claude-desktop/claude-desktop", true},
		{"claude-desktop", "/opt/Claude/claude-desktop", true},
		{"claude", "/tmp/.mount_Claude1a2b/claude", true},
		{"claude", "/home/x/.local/share/claude/versions/2.1.330", false},
		{"claude", "/home/x/.local/bin/claude", false},
		{"claude", "/home/x/.config/Claude/claude-code/2.1.187/claude", false},
		{"chrome_crashpad", "/usr/lib/claude-desktop/chrome_crashpad_handler", false},
	}
	for _, tc := range cases {
		if got := isClaudeDesktopProcess(tc.name, tc.exe); got != tc.want {
			t.Errorf("isClaudeDesktopProcess(%q, %q) = %v, want %v", tc.name, tc.exe, got, tc.want)
		}
	}
}
