package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// encryptV10 is the test's own encryptor (independent of decryptV10) so the
// round-trip actually exercises production decrypt logic against externally
// produced ciphertext, not just its own inverse.
func encryptV10(t *testing.T, secret, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(deriveAESKey(secret))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return append(append([]byte(nil), v10Prefix...), ct...)
}

func TestDecryptV10RoundTrip(t *testing.T) {
	secret := []byte("test-keychain-secret-value")
	plaintext := []byte(`{"acct:org:https://api.anthropic.com:scopes":{"token":"sk-ant-test"}}`)

	blob := encryptV10(t, secret, plaintext)

	got, err := decryptV10(secret, blob)
	if err != nil {
		t.Fatalf("decryptV10: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decryptV10 = %q, want %q", got, plaintext)
	}
}

func TestDecryptV10BadPrefix(t *testing.T) {
	if _, err := decryptV10([]byte("secret"), []byte("not-v10-prefixed-data-at-all!!!")); err == nil {
		t.Fatal("expected error for missing v10 prefix")
	}
}

func TestDecryptV10WrongSecret(t *testing.T) {
	blob := encryptV10(t, []byte("right-secret"), []byte("0123456789ABCDEF"))
	if _, err := decryptV10([]byte("wrong-secret-wrong"), blob); err == nil {
		t.Fatal("expected error decrypting with the wrong secret")
	}
}

func TestPickToken(t *testing.T) {
	if _, _, ok := pickToken(map[string]TokenEntry{}); ok {
		t.Fatal("pickToken on empty map should return ok=false")
	}

	single := map[string]TokenEntry{"a:org:base:scope": {Token: "only", ExpiresAt: 100}}
	key, entry, ok := pickToken(single)
	if !ok || key != "a:org:base:scope" || entry.Token != "only" {
		t.Fatalf("pickToken(single) = %q, %+v, %v", key, entry, ok)
	}

	multi := map[string]TokenEntry{
		"b-key": {Token: "older", ExpiresAt: 100},
		"a-key": {Token: "newer", ExpiresAt: 200},
		"c-key": {Token: "oldest", ExpiresAt: 50},
	}
	key, entry, ok = pickToken(multi)
	if !ok || key != "a-key" || entry.Token != "newer" {
		t.Fatalf("pickToken(multi) should pick furthest-future expiry: got %q, %+v", key, entry)
	}

	tie := map[string]TokenEntry{
		"z-key": {Token: "z", ExpiresAt: 100},
		"a-key": {Token: "a", ExpiresAt: 100},
	}
	key, _, ok = pickToken(tie)
	if !ok || key != "a-key" {
		t.Fatalf("pickToken(tie) should break ties by smallest key: got %q", key)
	}
}

// TestConfigFileBase64Decoding documents/pins the encoding assumption used by
// secret_darwin.go: a Node/Electron app persisting a safeStorage-encrypted
// binary blob into JSON text must base64-encode it, and typing the Go field
// as []byte makes encoding/json decode that automatically.
func TestConfigFileBase64Decoding(t *testing.T) {
	type configFile struct {
		TokenCacheV2 []byte `json:"oauth:tokenCacheV2"`
	}
	blob := []byte("v10\x00\x01\x02arbitrary-binary-data")
	data, err := json.Marshal(map[string]string{
		"oauth:tokenCacheV2": base64.StdEncoding.EncodeToString(blob),
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !bytes.Equal(cfg.TokenCacheV2, blob) {
		t.Fatalf("TokenCacheV2 = %q, want %q", cfg.TokenCacheV2, blob)
	}
}
