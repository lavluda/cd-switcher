//go:build darwin

package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// keychainSecret is a var (not a direct call) so tests can substitute a
// fixture instead of touching the real Keychain.
var keychainSecret = readKeychainSecret

// keychainTimeout bounds the Keychain read, including the one-time
// permission-prompt dialog it may trigger. Long enough for a user to notice
// and click Allow; short enough that a dismissed/ignored prompt doesn't wedge
// a stats-refresh goroutine forever.
const keychainTimeout = 30 * time.Second

// readKeychainSecret reads the per-app Safe Storage secret Claude Desktop
// registers in the login Keychain. The first call may trigger a one-time
// macOS permission prompt.
func readKeychainSecret() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password",
		"-w", "-s", "Claude Safe Storage", "-a", "Claude Key").Output()
	if err != nil {
		return nil, fmt.Errorf("secret: read Keychain secret: %w", err)
	}
	return bytes.TrimRight(out, "\n"), nil
}

// configFile is the subset of a Claude Desktop config.json this package
// reads. The field is typed []byte so encoding/json base64-decodes the
// Electron safeStorage-encrypted blob for us.
type configFile struct {
	TokenCacheV2 []byte `json:"oauth:tokenCacheV2"`
}

func sessionCredential(snapshotDir string) (Credential, error) {
	secret, err := keychainSecret()
	if err != nil {
		return Credential{}, err
	}
	defer zero(secret)

	data, err := os.ReadFile(filepath.Join(snapshotDir, "config.json"))
	if err != nil {
		return Credential{}, fmt.Errorf("%w: read config.json: %v", ErrNoCredential, err)
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Credential{}, fmt.Errorf("%w: parse config.json (tokenCacheV2 not valid base64 — encoding assumption may be wrong): %v", ErrNoCredential, err)
	}
	if len(cfg.TokenCacheV2) == 0 {
		return Credential{}, ErrNoCredential
	}

	plaintext, err := decryptV10(secret, cfg.TokenCacheV2)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: decrypt tokenCacheV2: %v", ErrNoCredential, err)
	}
	defer zero(plaintext)

	var cache map[string]TokenEntry
	if err := json.Unmarshal(plaintext, &cache); err != nil {
		return Credential{}, fmt.Errorf("%w: parse token cache: %v", ErrNoCredential, err)
	}

	_, entry, ok := pickToken(cache)
	if !ok {
		return Credential{}, ErrNoCredential
	}
	return Credential{Token: entry.Token, ExpiresAt: time.UnixMilli(entry.ExpiresAt)}, nil
}

// zero best-effort clears sensitive bytes after use. Go's GC may have already
// made copies (e.g. during append/growslice), so this is defense-in-depth,
// not a guarantee.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
