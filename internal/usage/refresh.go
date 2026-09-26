package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// desktopClientID is Claude Desktop's production OAuth client. It is embedded
// in the app and is not a user secret.
const desktopClientID = "89355bc3-cbfd-4382-905b-976645cad410"

const tokenPath = "/v1/oauth/token"

// defaultScope is the Desktop inference scope used when a cache key does not
// carry one.
const defaultScope = "user:inference"

// Refreshed is a new access grant from the token endpoint.
type Refreshed struct {
	Access    string
	Refresh   string
	ExpiresAt time.Time
}

// Refresh exchanges a refresh token for a new access token. A non-empty
// Refresh in the result means the server rotated the refresh token and the
// caller should store it.
func (c *Client) Refresh(ctx context.Context, refreshToken, scope string) (Refreshed, error) {
	if scope == "" {
		scope = defaultScope
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     desktopClientID,
		"refresh_token": refreshToken,
		"scope":         scope,
	})
	if err != nil {
		return Refreshed{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+tokenPath, bytes.NewReader(body))
	if err != nil {
		return Refreshed{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.doer.Do(req)
	if err != nil {
		return Refreshed{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Refreshed{}, fmt.Errorf("%w: refresh status %d", ErrUnavailable, resp.StatusCode)
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Refreshed{}, fmt.Errorf("%w: malformed refresh response: %v", ErrUnavailable, err)
	}
	if parsed.AccessToken == "" {
		return Refreshed{}, fmt.Errorf("%w: refresh response missing access token", ErrUnavailable)
	}
	exp := time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	if parsed.ExpiresIn <= 0 {
		exp = time.Now().Add(time.Hour)
	}
	return Refreshed{Access: parsed.AccessToken, Refresh: parsed.RefreshToken, ExpiresAt: exp}, nil
}

// ScopeFromCacheKey pulls the OAuth scope off an oauth:tokenCacheV2 key.
// Keys look like "account:org:https://api.anthropic.com:user:inference".
func ScopeFromCacheKey(key string) string {
	i := strings.Index(key, "https://")
	if i < 0 {
		i = strings.Index(key, "http://")
	}
	if i < 0 {
		return defaultScope
	}
	rest := key[i:]
	slash := strings.Index(rest, "://")
	if slash < 0 {
		return defaultScope
	}
	hostAndScope := rest[slash+3:]
	colon := strings.Index(hostAndScope, ":")
	if colon < 0 || colon == len(hostAndScope)-1 {
		return defaultScope
	}
	return hostAndScope[colon+1:]
}
