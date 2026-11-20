//go:build darwin

// Package keychain reads and writes macOS generic-password items through
// /usr/bin/security, the same items Claude Code and Codex keep logins in.
package keychain

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Supported reports whether this platform has a keychain to use.
func Supported() bool { return true }

// Read returns the password data of the generic-password item for
// service, matching on account too when one is given.
func Read(service, account string) (string, bool, error) {
	args := []string{"find-generic-password", "-s", service, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("/usr/bin/security", args...)
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

// Write sets the item's data, then reads it back to confirm it landed
// intact. Data goes to `security -i` on stdin so it stays out of the process
// list, but that mode silently truncates lines past ~4 KB, so a longer value
// falls back to an argument; the read-back catches any corruption either way.
func Write(service, account, data string) error {
	h := hex.EncodeToString([]byte(data))
	var cmd *exec.Cmd
	if len(h) <= 3500 {
		cmd = exec.Command("/usr/bin/security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %q -a %q -X %s\n", service, account, h))
	} else {
		cmd = exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", service, "-a", account, "-X", h)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write keychain %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	got, found, err := Read(service, account)
	if err != nil {
		return err
	}
	if !found || got != data {
		return errors.New("keychain write for " + service + " did not read back intact")
	}
	return nil
}

// Remove deletes the item; one that is already gone is not an error.
func Remove(service, account string) error {
	args := []string{"delete-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	out, err := exec.Command("/usr/bin/security", args...).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "could not be found") {
		return fmt.Errorf("delete keychain %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}
