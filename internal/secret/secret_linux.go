//go:build linux

package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// linuxSecret is a var so tests can substitute a fixture password.
var linuxSecret = readLinuxSecret

func sessionCredential(snapshotDir string) (Credential, error) {
	osSecret, err := linuxSecret()
	if err != nil {
		return Credential{}, err
	}
	defer zero(osSecret)
	return credentialFromV10File(filepath.Join(snapshotDir, "config.json"), osSecret)
}

func updateStoredToken(snapshotDir string, cred Credential) error {
	osSecret, err := linuxSecret()
	if err != nil {
		return err
	}
	defer zero(osSecret)
	return writeCredentialFile(filepath.Join(snapshotDir, "config.json"), osSecret, cred)
}

func credentialFromV10File(path string, osSecret []byte) (Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: read config.json: %v", ErrNoCredential, err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Credential{}, fmt.Errorf("%w: parse config.json: %v", ErrNoCredential, err)
	}
	if len(cfg.TokenCacheV2) == 0 {
		return Credential{}, ErrNoCredential
	}
	plaintext, err := decryptV10(osSecret, cfg.TokenCacheV2)
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

// readLinuxSecret looks up the Electron/Chromium safe-storage password in
// the session keyring. Claude Desktop's attribute value varies by build, so
// a few known ones are tried.
func readLinuxSecret() ([]byte, error) {
	lookups := [][]string{
		{"xdg:schema", "chrome_libsecret_os_crypt_password_v2", "application", "claude"},
		{"xdg:schema", "chrome_libsecret_os_crypt_password_v2", "application", "Claude"},
		{"xdg:schema", "chrome_libsecret_os_crypt_password_v2", "application", "chromium"},
	}
	var lastErr error
	for _, args := range lookups {
		out, err := exec.Command("secret-tool", append([]string{"lookup"}, args...)...).Output()
		if err != nil {
			lastErr = err
			continue
		}
		out = bytes.TrimRight(out, "\n")
		if len(out) > 0 {
			return out, nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("secret: libsecret lookup failed: %w", lastErr)
	}
	return nil, fmt.Errorf("secret: libsecret password not found")
}
