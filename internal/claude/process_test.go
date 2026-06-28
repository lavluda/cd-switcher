package claude

import "testing"

// TestIsClaudeDesktopProcess guards the distinction between Claude Desktop
// (which we manage) and the bundled "claude" CLI (Claude Code). Both run under
// an executable named "claude"; treating the CLI as Desktop made Quit() wait
// forever for it to exit and aborted every switch.
func TestIsClaudeDesktopProcess(t *testing.T) {
	cases := []struct {
		name string
		exe  string
		want bool
	}{
		{
			name: "Claude",
			exe:  "/Applications/Claude.app/Contents/MacOS/Claude",
			want: true,
		},
		{
			name: "Claude Helper (Renderer)",
			exe:  "/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/Contents/MacOS/Claude Helper (Renderer)",
			want: true,
		},
		{
			// Claude Code: shares the "claude" executable name but lives under
			// the data dir's claude-code/ tree. Must NOT count as Desktop.
			name: "claude",
			exe:  "/Users/x/Library/Application Support/Claude/claude-code/2.1.187/claude.app/Contents/MacOS/claude",
			want: false,
		},
		{
			// npm/global Claude Code install.
			name: "claude",
			exe:  "/Users/x/.local/share/node_modules/@anthropic-ai/claude-code/cli.js",
			want: false,
		},
		{
			name: "node",
			exe:  "/usr/local/bin/node",
			want: false,
		},
		{
			// Desktop main with no readable exe path still matches on name.
			name: "Claude",
			exe:  "",
			want: true,
		},
	}
	for _, tc := range cases {
		if got := isClaudeDesktopProcess(tc.name, tc.exe); got != tc.want {
			t.Errorf("isClaudeDesktopProcess(%q, %q) = %v, want %v", tc.name, tc.exe, got, tc.want)
		}
	}
}
