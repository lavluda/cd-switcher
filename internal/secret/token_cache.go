package secret

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// configFile is the subset of a Claude Desktop config.json this package
// reads. The field is typed []byte so encoding/json base64-decodes the
// Electron safeStorage-encrypted blob.
type configFile struct {
	TokenCacheV2 []byte `json:"oauth:tokenCacheV2"`
}

func credentialFromCache(cache map[string]TokenEntry) (Credential, error) {
	key, entry, ok := pickToken(cache)
	if !ok {
		return Credential{}, ErrNoCredential
	}
	return Credential{
		CacheKey:         key,
		Token:            entry.Token,
		RefreshToken:     entry.RefreshToken,
		ExpiresAt:        time.UnixMilli(entry.ExpiresAt),
		SubscriptionType: entry.SubscriptionType,
		RateLimitTier:    entry.RateLimitTier,
	}, nil
}

// writeCredentialFile replaces the picked token entry inside config.json and
// leaves every other key untouched. The file is replaced atomically.
func writeCredentialFile(path string, osSecret []byte, cred Credential) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: read config.json: %v", ErrNoCredential, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: parse config.json: %v", ErrNoCredential, err)
	}
	raw, ok := doc["oauth:tokenCacheV2"]
	if !ok {
		return ErrNoCredential
	}
	var b64 string
	if err := json.Unmarshal(raw, &b64); err != nil {
		return fmt.Errorf("%w: token cache encoding: %v", ErrNoCredential, err)
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("%w: token cache base64: %v", ErrNoCredential, err)
	}
	plaintext, err := decryptV10(osSecret, blob)
	if err != nil {
		return fmt.Errorf("%w: decrypt token cache: %v", ErrNoCredential, err)
	}
	defer zero(plaintext)

	var cache map[string]TokenEntry
	if err := json.Unmarshal(plaintext, &cache); err != nil {
		return fmt.Errorf("%w: parse token cache: %v", ErrNoCredential, err)
	}
	entry, ok := cache[cred.CacheKey]
	if !ok {
		return ErrNoCredential
	}
	entry.Token = cred.Token
	entry.RefreshToken = cred.RefreshToken
	entry.ExpiresAt = cred.ExpiresAt.UnixMilli()
	cache[cred.CacheKey] = entry

	updated, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	defer zero(updated)
	sealed, err := sealV10(osSecret, updated)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	if err != nil {
		return err
	}
	doc["oauth:tokenCacheV2"] = encoded
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
