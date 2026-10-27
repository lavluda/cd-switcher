//go:build darwin

// Package selfupdate replaces the running CD-Switcher.app with a newer release
// downloaded from GitHub, then relaunches it.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Supported reports whether in-place updates exist on this platform.
func Supported() bool { return true }

// maxDownload caps the dmg so a bad response can't fill the disk.
const maxDownload = 200 << 20

// DMGURL is where a release's macOS disk image is published.
func DMGURL(tag string) string {
	return "https://github.com/lavluda/cd-switcher/releases/download/" + tag + "/cd-switcher-darwin-universal.dmg"
}

// AppBundle returns the .app the running binary lives in, or an error when it
// is a bare binary (a dev build) that there is nothing to replace.
func AppBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // X.app/Contents/MacOS/bin
	if !strings.HasSuffix(app, ".app") {
		return "", errors.New("not running from an app bundle")
	}
	return app, nil
}

// Install downloads the dmg at url, stages the app next to the running one,
// and starts a detached helper that swaps it in once this process has exited.
// The caller must quit right after Install returns nil.
func Install(ctx context.Context, url string) error {
	app, err := AppBundle()
	if err != nil {
		return err
	}
	staged, err := Stage(ctx, url, app)
	if err != nil {
		return err
	}
	// Wait for us to exit, swap, and fall back to the old copy if the move
	// fails. Setsid lets the helper outlive this process.
	const script = `while kill -0 "$1" 2>/dev/null; do sleep 0.3; done
if mv "$2" "$2.old"; then mv "$3" "$2" || mv "$2.old" "$2"; fi
rm -rf "$2.old" "$(dirname "$3")"
open "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), app, staged)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start update helper: %w", err)
	}
	return nil
}

// Stage downloads the dmg and copies its CD-Switcher.app into a fresh hidden
// directory beside app (same volume, so the later swap is a rename), after
// checking its code signature. It returns the staged .app path.
func Stage(ctx context.Context, url, app string) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(app), ".cd-switcher-update-")
	if err != nil {
		return "", fmt.Errorf("cannot write next to %s: %w", app, err)
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()

	dmg := filepath.Join(dir, "update.dmg")
	if err := download(ctx, url, dmg); err != nil {
		return "", err
	}
	mnt := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		return "", err
	}
	if out, err := exec.Command("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return "", fmt.Errorf("mount update: %v: %s", err, strings.TrimSpace(string(out)))
	}
	defer exec.Command("/usr/bin/hdiutil", "detach", "-force", mnt).Run()

	staged := filepath.Join(dir, filepath.Base(app))
	if out, err := exec.Command("/usr/bin/ditto", filepath.Join(mnt, filepath.Base(app)), staged).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy update: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", staged).CombinedOutput(); err != nil {
		return "", fmt.Errorf("update failed signature check: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// --verify accepts any valid signature, ad hoc ones included. Once the
	// installed app carries a Developer ID, an update must come from the same
	// team, or a swapped download would pass.
	if want := teamID(app); want != "" && teamID(staged) != want {
		return "", fmt.Errorf("update is not signed by team %s", want)
	}
	ok = true
	return staged, nil
}

// teamID is the Developer ID team that signed the app bundle, or "" for an
// unsigned or ad-hoc-signed one.
func teamID(app string) string {
	out, _ := exec.Command("/usr/bin/codesign", "-dv", "--verbose=2", app).CombinedOutput()
	return parseTeamID(string(out))
}

func parseTeamID(codesignOutput string) string {
	for _, line := range strings.Split(codesignOutput, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok && v != "not set" {
			return v
		}
	}
	return ""
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: status %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return err
	}
	if n > maxDownload {
		return errors.New("update download is larger than expected")
	}
	return nil
}
