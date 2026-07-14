package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// v10Prefix marks Chromium's os_crypt v10 scheme (macOS/Linux Safe Storage).
var v10Prefix = []byte("v10")

// deriveAESKey derives the AES-128 key Chromium/Electron use to wrap the
// per-app Safe Storage secret: PBKDF2-HMAC-SHA1(secret, "saltysalt", 1003, 16).
func deriveAESKey(secret []byte) []byte {
	return pbkdf2.Key(secret, []byte("saltysalt"), 1003, 16, sha1.New)
}

// decryptV10 decrypts a Chromium "v10"-prefixed ciphertext blob: AES-128-CBC
// with a fixed IV of 16 spaces (0x20), PKCS7-unpadded.
func decryptV10(secret, blob []byte) ([]byte, error) {
	if !bytes.HasPrefix(blob, v10Prefix) {
		return nil, fmt.Errorf("secret: ciphertext missing %q prefix", v10Prefix)
	}
	ct := blob[len(v10Prefix):]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("secret: ciphertext length %d is not a multiple of the block size", len(ct))
	}

	block, err := aes.NewCipher(deriveAESKey(secret))
	if err != nil {
		return nil, err
	}
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)

	return pkcs7Unpad(pt)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	n := len(data)
	if n == 0 {
		return nil, fmt.Errorf("secret: empty plaintext")
	}
	pad := int(data[n-1])
	if pad == 0 || pad > aes.BlockSize || pad > n {
		return nil, fmt.Errorf("secret: invalid PKCS7 padding")
	}
	for _, b := range data[n-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("secret: invalid PKCS7 padding")
		}
	}
	return data[:n-pad], nil
}

// pickToken deterministically selects one entry from a decrypted
// oauth:tokenCacheV2 map: the entry with the furthest-future ExpiresAt (most
// likely still valid), ties broken by the lexicographically smallest key.
func pickToken(cache map[string]TokenEntry) (key string, entry TokenEntry, ok bool) {
	for k, e := range cache {
		if !ok || e.ExpiresAt > entry.ExpiresAt || (e.ExpiresAt == entry.ExpiresAt && k < key) {
			key, entry, ok = k, e, true
		}
	}
	return key, entry, ok
}
