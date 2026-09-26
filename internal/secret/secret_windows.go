//go:build windows

package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func sessionCredential(snapshotDir string) (Credential, error) {
	blob, err := readTokenBlob(snapshotDir)
	if err != nil {
		return Credential{}, err
	}
	plaintext, err := decryptWindows(snapshotDir, blob)
	if err != nil {
		return Credential{}, err
	}
	defer zero(plaintext)

	var cache map[string]TokenEntry
	if err := json.Unmarshal(plaintext, &cache); err != nil {
		return Credential{}, fmt.Errorf("%w: parse token cache: %v", ErrNoCredential, err)
	}
	return credentialFromCache(cache)
}

func updateStoredToken(snapshotDir string, cred Credential) error {
	// Windows safeStorage is DPAPI or AES-GCM, not the macOS v10-CBC scheme
	// writeCredentialFile knows how to reseal. Refresh still works in memory;
	// persisting a rotated refresh token needs the same scheme Claude wrote.
	if cred.CacheKey == "" || snapshotDir == "" {
		return ErrNoCredential
	}
	return fmt.Errorf("secret: writing refreshed tokens is not supported on Windows")
}

func readTokenBlob(snapshotDir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(snapshotDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: read config.json: %v", ErrNoCredential, err)
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%w: parse config.json: %v", ErrNoCredential, err)
	}
	if len(cfg.TokenCacheV2) == 0 {
		return nil, ErrNoCredential
	}
	return cfg.TokenCacheV2, nil
}

// decryptWindows handles both Electron safeStorage shapes on Windows:
// Chromium "v10" AES-GCM whose key is DPAPI-wrapped in Local State, and a
// raw DPAPI blob.
func decryptWindows(snapshotDir string, blob []byte) ([]byte, error) {
	if bytes.HasPrefix(blob, v10Prefix) {
		key, err := windowsAESKey(snapshotDir)
		if err != nil {
			return nil, err
		}
		defer zero(key)
		plain, err := decryptAESGCM(key, blob[len(v10Prefix):])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoCredential, err)
		}
		return plain, nil
	}
	plain, err := dpapiDecrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("%w: DPAPI: %v", ErrNoCredential, err)
	}
	return plain, nil
}

func windowsAESKey(snapshotDir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(snapshotDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("%w: read Local State: %v", ErrNoCredential, err)
	}
	var doc struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: parse Local State: %v", ErrNoCredential, err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("%w: Local State key: %v", ErrNoCredential, err)
	}
	const dpapiPrefix = "DPAPI"
	if !bytes.HasPrefix(raw, []byte(dpapiPrefix)) {
		return nil, fmt.Errorf("%w: Local State key is not DPAPI-wrapped", ErrNoCredential)
	}
	return dpapiDecrypt(raw[len(dpapiPrefix):])
}

// decryptAESGCM expects Chromium's layout: 12-byte nonce, then ciphertext
// with the GCM tag appended.
func decryptAESGCM(key, payload []byte) ([]byte, error) {
	if len(payload) < 12+16 {
		return nil, fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, ct := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func dpapiDecrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty blob")
	}
	crypt32 := windows.NewLazySystemDLL("crypt32.dll")
	proc := crypt32.NewProc("CryptUnprotectData")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	localFree := kernel32.NewProc("LocalFree")

	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := proc.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, err
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	src := unsafe.Slice(out.pbData, out.cbData)
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst, nil
}
