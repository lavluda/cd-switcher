// Package terminal opens a command in the user's terminal app.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Apps lists the terminals that can be chosen on this platform. The first is
// the default.
func Apps() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"Terminal", "iTerm", "Ghostty", "WezTerm", "kitty", "Alacritty"}
	case "windows":
		return []string{"Windows Terminal", "Command Prompt"}
	}
	return []string{"Default terminal"}
}

func shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// keepOpen runs command in a login shell, then leaves an interactive shell
// behind so the window does not close when the command exits.
func keepOpen(command string) []string {
	sh := shell()
	return []string{sh, "-lc", command + "; exec " + sh + " -l"}
}

func appleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Open runs command in a new window of app, in the user's home folder.
func Open(app, command string) error {
	home, _ := os.UserHomeDir()
	full := "cd " + quote(home) + " && " + command
	switch runtime.GOOS {
	case "darwin":
		switch app {
		case "", "Terminal":
			return run("osascript", "-e", `tell application "Terminal"`, "-e", "activate", "-e", "do script "+appleScriptString(full), "-e", "end tell")
		case "iTerm":
			return run("osascript", "-e", `tell application "iTerm"`, "-e", "activate",
				"-e", "set w to (create window with default profile)",
				"-e", "tell current session of w to write text "+appleScriptString(full), "-e", "end tell")
		case "Ghostty", "Alacritty":
			return run("open", append([]string{"-na", app, "--args", "-e"}, keepOpen(full)...)...)
		case "WezTerm":
			return run("open", append([]string{"-na", app, "--args", "start", "--"}, keepOpen(full)...)...)
		case "kitty":
			return run("open", append([]string{"-na", app, "--args"}, keepOpen(full)...)...)
		}
		return fmt.Errorf("unknown terminal %q", app)
	case "windows":
		if app == "Windows Terminal" {
			if err := run("wt.exe", "-d", home, "cmd", "/k", command); err == nil {
				return nil
			}
		}
		return run("cmd", "/c", "start", "", "/d", home, "cmd", "/k", command)
	}
	for _, t := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xterm"} {
		if _, err := exec.LookPath(t); err != nil {
			continue
		}
		if t == "gnome-terminal" {
			return run(t, append([]string{"--"}, keepOpen(full)...)...)
		}
		return run(t, append([]string{"-e"}, keepOpen(full)...)...)
	}
	return errors.New("no terminal emulator found")
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
