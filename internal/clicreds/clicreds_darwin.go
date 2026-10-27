//go:build darwin

// Package clicreds lets each CD-Switcher account remember its own Claude Code
// (CLI) login. Claude Code keeps its OAuth login in one macOS Keychain item;
// Save copies that item into a per-account item of our own and Restore puts
// an account's copy back, so the CLI follows the account that is switched to.
package clicreds

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

const liveService = "Claude Code-credentials"

// Supported reports whether this platform can swap the CLI login.
func Supported() bool { return true }

func storedService(id string) string { return "CD-Switcher CLI " + id }

// Save copies the live CLI login into the account's own item. It does
// nothing if the CLI is not logged in.
func Save(id string) error {
	blob, found, err := read(liveService)
	if err != nil || !found {
		return err
	}
	return write(storedService(id), blob)
}

// Restore puts the account's saved CLI login back as the live one and reports
// whether it had one. An account with none leaves the CLI logged out, so it
// never inherits the previous account's login.
func Restore(id string) (bool, error) {
	blob, found, err := read(storedService(id))
	if err != nil {
		return false, err
	}
	if !found {
		return false, remove(liveService)
	}
	return true, write(liveService, blob)
}

// Forget deletes the account's saved CLI login.
func Forget(id string) error { return remove(storedService(id)) }

// read returns the password data of the generic-password item for service.
func read(service string) (string, bool, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-w")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "could not be found") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read keychain %q: %v: %s", service, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSuffix(string(out), "\n"), true, nil
}

// write sets the item's data, then reads it back to confirm it landed intact.
// Data goes to `security -i` on stdin so it stays out of the process list, but
// that mode silently truncates lines past ~4 KB, so a longer value falls back
// to an argument; the read-back catches any corruption either way.
func write(service, data string) error {
	acct := "claude-code"
	if u, err := user.Current(); err == nil {
		acct = u.Username
	}
	h := hex.EncodeToString([]byte(data))
	args := []string{"add-generic-password", "-U", "-s", service, "-a", acct, "-X", h}

	var cmd *exec.Cmd
	if len(h) <= 3500 {
		cmd = exec.Command("/usr/bin/security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %q -a %q -X %s\n", service, acct, h))
	} else {
		cmd = exec.Command("/usr/bin/security", args...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write keychain %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	got, found, err := read(service)
	if err != nil {
		return err
	}
	if !found || got != data {
		return errors.New("keychain write for " + service + " did not read back intact")
	}
	return nil
}

// remove deletes the item; one that is already gone is not an error.
func remove(service string) error {
	out, err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", service).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "could not be found") {
		return fmt.Errorf("delete keychain %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}
