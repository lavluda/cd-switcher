//go:build darwin

// Package clicreds lets each CD-Switcher account remember its own Claude Code
// (CLI) login. Claude Code keeps its OAuth login in one macOS Keychain item and
// the matching profile under "oauthAccount" in ~/.claude.json. Save copies the
// account's part of both into an item of our own; Restore puts an account's
// copy back, so the CLI follows the account that is switched to.
//
// Only the account keys move. Everything else in the live item (MCP OAuth
// grants, plugin secrets) belongs to this machine and stays put. Writes happen
// under Claude Code's own locks so a token refresh can never interleave, and a
// small journal lets the next launch finish a switch that was cut off.
package clicreds

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/fslock"
)

const liveService = "Claude Code-credentials"

// accountKeys follow the account; the same set Claude Code's /login clears.
var accountKeys = []string{
	"claudeAiOauth",
	"organizationUuid",
	"trustedDeviceToken",
	"enterpriseGateway",
	"designOauth",
	"trustedDeviceEnrolledAfterRefusalAtMs",
	"cloudSessionsOauth",
}

// cacheKeys in ~/.claude.json describe the signed-in account; Claude Code
// clears the same set on logout and fetches them again.
var cacheKeys = []string{
	"additionalModelOptionsCache",
	"additionalModelOptionsAnsweredAt",
	"additionalModelCostsCache",
	"modelAccessCache",
	"orgModelDefaultCache",
	"cachedArtifactRoster",
	"artifactRosterDenied",
	"lastSeenOrgDefaultUpdatedAt",
	"clientDataCache",
	"clientDataCacheSlots",
	"autoCompactWindowsCache",
	"cachedUsageUtilization",
	"metricsStatusCache",
	"metricsStatusCacheByPrincipal",
	"githubWebConnectionStatusCache",
	"unusedLimitResetStatusCache",
	"startupPrefetchedAt",
	"cachedExtraUsageDisabledReason",
	"penguinModeOrgEnabled",
}

// profileKey carries the account's ~/.claude.json "oauthAccount" inside our
// own stored item, next to its credential keys.
const profileKey = "cdswitcherOauthAccount"

const journalName = "cli-switch.journal"

// home is swapped in tests.
var home = os.UserHomeDir

// Supported reports whether this platform can swap the CLI login.
func Supported() bool { return true }

func storedService(id string) string { return "CD-Switcher CLI " + id }

// Save copies the live CLI login's account keys, plus its profile, into the
// account's own item. A CLI with no login clears the account's item, so the
// account later restores as signed out rather than as a stale login.
func Save(id string) error {
	h, err := home()
	if err != nil {
		return err
	}
	blob, found, err := read(liveService, keychainAccount())
	if err != nil {
		return err
	}
	acct := map[string]json.RawMessage{}
	if found {
		live, err := parseObject(blob)
		if err != nil {
			return fmt.Errorf("live Claude Code login: %w", err)
		}
		acct = pick(live, accountKeys)
	}
	if len(acct) == 0 {
		return remove(storedService(id), "")
	}
	if doc, err := readJSONFile(globalConfig(h)); err == nil {
		if p, ok := doc["oauthAccount"]; ok {
			acct[profileKey] = p
		}
	}
	out, err := json.Marshal(acct)
	if err != nil {
		return err
	}
	return write(storedService(id), keychainAccount(), string(out))
}

// Restore makes the account's saved CLI login the live one and reports whether
// it had one. An account with none leaves the CLI signed out, so it never
// inherits the previous account's login. stateDir holds the journal.
func Restore(stateDir, id string) (bool, error) {
	journal := filepath.Join(stateDir, journalName)
	if err := os.WriteFile(journal, []byte(id), 0o600); err != nil {
		return false, err
	}
	found, err := commit(id)
	if err != nil {
		return false, err // the journal stays, so Recover retries on next launch
	}
	return found, os.Remove(journal)
}

// Recover finishes a Restore that was interrupted, for instance by a crash
// between the Keychain write and the ~/.claude.json write.
func Recover(stateDir string) error {
	b, err := os.ReadFile(filepath.Join(stateDir, journalName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = Restore(stateDir, strings.TrimSpace(string(b)))
	return err
}

// Forget deletes the account's saved CLI login.
func Forget(id string) error { return remove(storedService(id), "") }

func commit(id string) (bool, error) {
	h, err := home()
	if err != nil {
		return false, err
	}
	blob, found, err := read(storedService(id), "")
	if err != nil {
		return false, err
	}
	stored := map[string]json.RawMessage{}
	if found {
		if stored, err = parseObject(blob); err != nil {
			return false, fmt.Errorf("saved CLI login: %w", err)
		}
	}

	// Claude Code's refresh locks keep it from rotating the token while the
	// item changes under it. Both are held until the profile is written too.
	dir := filepath.Join(h, ".claude")
	refreshA, err := fslock.Acquire(filepath.Join(dir, ".oauth_refresh.lock"), 60*time.Second, 10*time.Second)
	if err != nil {
		return false, err
	}
	defer refreshA.Release()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	refreshB, err := fslock.Acquire(real+".lock", 60*time.Second, 10*time.Second)
	if err != nil {
		return false, err
	}
	defer refreshB.Release()

	if err := writeLive(dir, stored); err != nil {
		return false, err
	}
	return found, writeProfile(globalConfig(h), stored[profileKey])
}

// writeLive replaces the live item's account keys with the stored ones and
// keeps every other key, under Claude Code's storage lock.
func writeLive(dir string, stored map[string]json.RawMessage) error {
	lock, err := fslock.Acquire(filepath.Join(dir, ".storage-write.lock"), 15*time.Second, 10*time.Second)
	if err != nil {
		return err
	}
	defer lock.Release()

	acct := keychainAccount()
	blob, found, err := read(liveService, acct)
	if err != nil {
		return err
	}
	live := map[string]json.RawMessage{}
	if found {
		if live, err = parseObject(blob); err != nil {
			return fmt.Errorf("live Claude Code login: %w", err)
		}
	}
	merged := merge(live, stored)
	if len(merged) == 0 {
		return remove(liveService, acct)
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return write(liveService, acct, string(out))
}

// merge is live without its account keys, plus stored's account keys.
func merge(live, stored map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range live {
		out[k] = v
	}
	for _, k := range accountKeys {
		delete(out, k)
		if v, ok := stored[k]; ok {
			out[k] = v
		}
	}
	return out
}

func pick(m map[string]json.RawMessage, keys []string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// writeProfile sets (or, for nil, removes) "oauthAccount" in ~/.claude.json
// and drops the account caches, under Claude Code's config lock. Every other
// key is kept. A file that is not a JSON object is left alone.
func writeProfile(path string, account json.RawMessage) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // keep a dotfiles symlink pointing at the real file
	}
	lock, err := fslock.Acquire(path+".lock", 10*time.Second, 8*time.Second)
	if err != nil {
		return err
	}
	defer lock.Release()

	doc, err := readJSONFile(path)
	mode := fs.FileMode(0o600)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc = map[string]json.RawMessage{}
	case err != nil:
		return fmt.Errorf("%s: %w; left unchanged", path, err)
	default:
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	if account != nil {
		doc["oauthAccount"] = account
	} else {
		delete(doc, "oauthAccount")
	}
	for _, k := range cacheKeys {
		delete(doc, k)
	}
	if _, ok := doc["hasCompletedOnboarding"]; !ok {
		doc["hasCompletedOnboarding"] = json.RawMessage("true")
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".cdswitcher-tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// globalConfig is Claude Code's ~/.claude.json, or the legacy
// ~/.claude/.config.json when that one exists and is not empty.
func globalConfig(h string) string {
	legacy := filepath.Join(h, ".claude", ".config.json")
	if fi, err := os.Stat(legacy); err == nil && fi.Size() > 0 {
		return legacy
	}
	return filepath.Join(h, ".claude.json")
}

func readJSONFile(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseObject(string(b))
}

func parseObject(s string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, errors.New("not a JSON object")
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

// keychainAccount is the account Claude Code files its item under: $USER, or
// claude-code-user when that is empty or has characters it rejects.
func keychainAccount() string {
	u := os.Getenv("USER")
	if u == "" {
		if h, err := home(); err == nil {
			u = filepath.Base(h)
		}
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return "claude-code-user"
		}
	}
	if u == "" {
		return "claude-code-user"
	}
	return u
}

// Keychain access goes through these so tests can use a fake store.
var (
	read   = keychainRead
	write  = keychainWrite
	remove = keychainRemove
)

// keychainRead returns the password data of the generic-password item for
// service, matching on account too when one is given.
func keychainRead(service, account string) (string, bool, error) {
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

// keychainWrite sets the item's data, then reads it back to confirm it landed
// intact. Data goes to `security -i` on stdin so it stays out of the process
// list, but that mode silently truncates lines past ~4 KB, so a longer value
// falls back to an argument; the read-back catches any corruption either way.
func keychainWrite(service, account, data string) error {
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
	got, found, err := keychainRead(service, account)
	if err != nil {
		return err
	}
	if !found || got != data {
		return errors.New("keychain write for " + service + " did not read back intact")
	}
	return nil
}

// keychainRemove deletes the item; one that is already gone is not an error.
func keychainRemove(service, account string) error {
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
