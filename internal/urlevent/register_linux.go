package urlevent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const desktopFile = "cd-switcher-link.desktop"

// Register makes this executable the cdswitcher:// handler for the user,
// through a hidden .desktop entry and xdg-mime.
func Register(exe string) error {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(h, ".local", "share")
	}
	dir := filepath.Join(data, "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	quoted := `"` + strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(exe) + `"`
	entry := []byte("[Desktop Entry]\nType=Application\nName=CD-Switcher\nExec=" + quoted + " %u\nNoDisplay=true\nMimeType=x-scheme-handler/cdswitcher;\n")
	p := filepath.Join(dir, desktopFile)
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, entry) {
		return nil
	}
	if err := os.WriteFile(p, entry, 0o644); err != nil {
		return err
	}
	if _, err := exec.LookPath("xdg-mime"); err != nil {
		return nil // no xdg-utils: the .desktop entry alone works for most desktops
	}
	return exec.Command("xdg-mime", "default", desktopFile, "x-scheme-handler/cdswitcher").Run()
}
