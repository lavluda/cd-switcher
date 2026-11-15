package claude

import "testing"

// TestIsClaudeDesktopProcessLookalike covers the macOS-only rule: apps whose name
// merely starts with "Claude" (here the third-party "Claude Usage.app") are not
// Desktop. Linux and Windows match on the name prefix and do not have this rule.
func TestIsClaudeDesktopProcessLookalike(t *testing.T) {
	cases := []struct {
		name string
		exe  string
	}{
		// Must NOT match, or Quit() blocks until timeout waiting for a process it can't kill.
		{"Claude Usage", "/Applications/Claude Usage.app/Contents/MacOS/Claude Usage"},
		// Same lookalike with an unreadable exe path: rejected by name.
		{"Claude Usage", ""},
	}
	for _, tc := range cases {
		if isClaudeDesktopProcess(tc.name, tc.exe) {
			t.Errorf("isClaudeDesktopProcess(%q, %q) = true, want false", tc.name, tc.exe)
		}
	}
}
