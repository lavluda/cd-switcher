// Package secret decrypts Claude Desktop's locally cached OAuth token from a
// profile snapshot's config.json, using the OS's "safe storage" key. Unlike
// the rest of CD-Switcher (which only ever copies encrypted files around),
// this package decrypts credentials transiently, in memory, so usage stats
// can be fetched for display. Nothing decrypted is ever written to disk or
// logged.
package secret

import (
	"errors"
	"time"
)

// ErrUnsupported is returned on platforms where reading the session
// credential is not yet implemented.
var ErrUnsupported = errors.New("secret: reading credentials is not supported on this platform")

// ErrNoCredential is returned when a profile snapshot has no usable token.
var ErrNoCredential = errors.New("secret: no usable session credential found")

// TokenEntry is one entry of Claude Desktop's decrypted oauth:tokenCacheV2
// map, keyed by "<account>:<org>:<api-base>:<scopes>".
type TokenEntry struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresAt        int64  `json:"expiresAt"` // ms since epoch
	SubscriptionType string `json:"subscriptionType"`
	RateLimitTier    string `json:"rateLimitTier"`
}

// Credential is the bearer token needed to call Claude's usage API, plus its
// expiry so callers can judge freshness.
type Credential struct {
	Token     string
	ExpiresAt time.Time
}

// SessionCredential decrypts and returns the OAuth bearer token stored in the
// profile snapshot at snapshotDir (a profile.Store.ProfileDir(id) result).
// This is best-effort: only the active profile's token is guaranteed fresh,
// inactive profiles may already be expired. Callers must treat any error as
// "stats unavailable", not a hard failure.
func SessionCredential(snapshotDir string) (Credential, error) {
	return sessionCredential(snapshotDir)
}
