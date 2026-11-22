//go:build linux

package login

import (
	"os"
	"path/filepath"
	"strings"
)

// entryName is the XDG autostart file. Desktops that follow the XDG
// Autostart spec (GNOME, KDE, Xfce, Cinnamon, MATE, ...) start every entry
// in $XDG_CONFIG_HOME/autostart when the user logs in.
const entryName = "cd-switcher.desktop"

// Supported reports whether this OS can register a login item.
func Supported() bool { return true }

// Enable registers the current executable to launch at login.
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	path, err := entryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(entry(exe)), 0o644)
}

// Disable removes the login item.
func Disable() error {
	path, err := entryPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func entry(exe string) string {
	return "[Desktop Entry]\nType=Application\nName=CD-Switcher\nComment=Switch Claude Desktop accounts\nExec=" +
		execQuote(exe) + "\nTerminal=false\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n"
}

// execQuote quotes a path for an Exec key: the Desktop Entry spec's argument
// quoting, then its string escaping (which doubles each backslash again).
func execQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(s) + `"`
}

func entryPath() (string, error) {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "autostart", entryName), nil
}
