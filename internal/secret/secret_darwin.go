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
	"sync"
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

// keychainCache holds the secret after the first successful read. The key
// never changes while Claude is installed, and every profile's stats refresh
// used to spawn its own `security` call and its own permission prompt.
var keychainCache struct {
	sync.Mutex
	secret []byte
}

// readKeychainSecret reads the per-app Safe Storage secret Claude Desktop
// registers in the login Keychain, once per process. The first call may
// trigger a one-time macOS permission prompt. Callers get a copy they may
// zero.
func readKeychainSecret() ([]byte, error) {
	keychainCache.Lock()
	defer keychainCache.Unlock()
	if keychainCache.secret == nil {
		ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password",
			"-w", "-s", "Claude Safe Storage", "-a", "Claude Key").Output()
		if err != nil {
			return nil, fmt.Errorf("secret: read Keychain secret: %w", err)
		}
		keychainCache.secret = bytes.TrimRight(out, "\n")
	}
	return append([]byte(nil), keychainCache.secret...), nil
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

	return credentialFromCache(cache)
}

func updateStoredToken(snapshotDir string, cred Credential) error {
	osSecret, err := keychainSecret()
	if err != nil {
		return err
	}
	defer zero(osSecret)
	return writeCredentialFile(filepath.Join(snapshotDir, "config.json"), osSecret, cred)
}
