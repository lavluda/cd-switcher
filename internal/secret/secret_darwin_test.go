//go:build darwin

package secret

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionCredentialRoundTrip(t *testing.T) {
	secret := []byte("fixture-keychain-secret")
	orig := keychainSecret
	keychainSecret = func() ([]byte, error) { return secret, nil }
	t.Cleanup(func() { keychainSecret = orig })

	cache := map[string]TokenEntry{
		"acct:org:https://api.anthropic.com:scopes": {
			Token:     "sk-ant-fixture-token",
			ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
		},
	}
	plaintext, err := json.Marshal(cache)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	blob := encryptV10(t, secret, plaintext)

	cfgData, err := json.Marshal(map[string]string{
		"oauth:tokenCacheV2": base64.StdEncoding.EncodeToString(blob),
	})
	if err != nil {
		t.Fatalf("json.Marshal config: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgData, 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cred, err := sessionCredential(dir)
	if err != nil {
		t.Fatalf("sessionCredential: %v", err)
	}
	if cred.Token != "sk-ant-fixture-token" {
		t.Fatalf("cred.Token = %q, want sk-ant-fixture-token", cred.Token)
	}
}

func TestSessionCredentialKeychainError(t *testing.T) {
	orig := keychainSecret
	keychainSecret = func() ([]byte, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { keychainSecret = orig })

	if _, err := sessionCredential(t.TempDir()); err == nil {
		t.Fatal("expected error when Keychain read fails")
	}
}

func TestSessionCredentialMissingConfig(t *testing.T) {
	orig := keychainSecret
	keychainSecret = func() ([]byte, error) { return []byte("secret"), nil }
	t.Cleanup(func() { keychainSecret = orig })

	if _, err := sessionCredential(t.TempDir()); err == nil {
		t.Fatal("expected error when config.json is missing")
	}
}
