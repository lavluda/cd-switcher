package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/fslock"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/keychain"
)

// With cli_auth_credentials_store = "keyring" (or "auto", where a keychain
// exists) Codex keeps the live login in a keychain item instead of auth.json:
// service "Codex Auth", account "cli|" plus 16 hex of sha256(CODEX_HOME), the
// same JSON as auth.json (codex-rs/login/src/auth/storage.rs).
// shortcut: only the macOS Keychain is reached, and Codex's optional
// secret_auth_storage backend is not; add libsecret if Linux users ask.
const keyringService = "Codex Auth"

// Keychain access goes through these so tests can use a fake store.
var (
	kcRead  = keychain.Read
	kcWrite = keychain.Write
)

// storeMode is config.toml's cli_auth_credentials_store, "file" when unset.
func storeMode() string {
	b, _ := os.ReadFile(filepath.Join(harness.CodexHome(), "config.toml"))
	v, ok := cfgfile.TOMLRootValue(string(b), "cli_auth_credentials_store")
	if f := strings.Fields(v); ok && len(f) > 0 {
		return strings.Trim(f[0], `"'`)
	}
	return "file"
}

func useKeyring(mode string) bool {
	return mode == "keyring" || mode == "auto" && keychain.Supported()
}

// keyringKey is the keychain account Codex files the login for home under.
func keyringKey(home string) string {
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	sum := sha256.Sum256([]byte(home))
	return "cli|" + hex.EncodeToString(sum[:])[:16]
}

// LiveFile returns a file holding the login Codex uses now, and a cleanup to
// call when done. A keychain login is copied to a private temp file.
func LiveFile() (string, func(), error) {
	mode := storeMode()
	if !useKeyring(mode) {
		return LivePath(), func() {}, nil
	}
	data, found, err := kcRead(keyringService, keyringKey(harness.CodexHome()))
	if err != nil {
		return "", nil, fmt.Errorf("Codex keeps its login in the system keychain: %w", err)
	}
	if !found {
		if mode == "keyring" {
			return "", nil, fmt.Errorf("no Codex login in the keychain: %w", fs.ErrNotExist)
		}
		return LivePath(), func() {}, nil // "auto" falls back to the file
	}
	f, err := os.CreateTemp("", "cdswitcher-codex-*.json")
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// UseLogin makes the saved login at src the one Codex uses, wherever Codex
// keeps it, under Codex's auth.lock.
func UseLogin(src string) error {
	l, err := fslock.LockFile(filepath.Join(harness.CodexHome(), "auth.lock"), 10*time.Second)
	if err != nil {
		return err
	}
	defer l.Unlock()
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if _, err := cfgfile.ParseObject(b); err != nil {
		return fmt.Errorf("%s is not a Codex login: %w", src, err)
	}
	mode := storeMode()
	if !useKeyring(mode) {
		return cfgfile.WriteFile(LivePath(), b, true)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return err
	}
	if err := kcWrite(keyringService, keyringKey(harness.CodexHome()), compact.String()); err != nil {
		return err
	}
	// Codex does the same after a keychain save, so a stale file never wins.
	if err := os.Remove(LivePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
