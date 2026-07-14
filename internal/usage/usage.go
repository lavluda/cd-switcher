// Package usage calls Claude's undocumented usage-stats endpoint. All
// endpoint knowledge (URL, headers, response shape) lives here so a breaking
// API change is a one-file fix. The endpoint is undocumented and may change
// without notice; callers should poll infrequently and treat any failure as
// "stats unavailable", not a hard error.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	usagePath      = "/api/oauth/usage"
	defaultTimeout = 8 * time.Second
)

// ErrUnavailable wraps any failure to fetch or parse usage stats: network
// errors, non-2xx responses, and unexpected response shapes all count.
var ErrUnavailable = errors.New("usage: stats unavailable")

// Window is a usage window's utilization and next reset time.
type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// ExtraUsage describes pay-as-you-go usage beyond the plan's included limits.
// MonthlyLimit/UsedCredits are in minor currency units (e.g. pence, cents) —
// divide by 100 for a major-unit ($/£/€) amount.
type ExtraUsage struct {
	MonthlyLimit float64 `json:"monthly_limit"`
	UsedCredits  float64 `json:"used_credits"`
	Utilization  float64 `json:"utilization"`
	Currency     string  `json:"currency"`
}

// Limit is one active or upcoming rate/usage limit.
type Limit struct {
	Kind     string    `json:"kind"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity"`
	ResetsAt time.Time `json:"resets_at"`
	IsActive bool      `json:"is_active"`
}

// Stats is the parsed response of GET /api/oauth/usage.
type Stats struct {
	FiveHour   Window     `json:"five_hour"`
	SevenDay   Window     `json:"seven_day"`
	ExtraUsage ExtraUsage `json:"extra_usage"`
	Limits     []Limit    `json:"limits"`
}

// httpDoer is the seam tests use to inject a fake/httptest transport instead
// of making real network calls.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client fetches usage stats from Claude's API using an OAuth bearer token.
type Client struct {
	doer    httpDoer
	baseURL string
}

// NewClient returns a Client pointed at the real Claude API.
func NewClient() *Client {
	return &Client{doer: &http.Client{Timeout: defaultTimeout}, baseURL: defaultBaseURL}
}

// newClientWithBase builds a Client against an arbitrary base URL (tests only).
func newClientWithBase(baseURL string, doer httpDoer) *Client {
	return &Client{doer: doer, baseURL: baseURL}
}

// Fetch calls GET /api/oauth/usage with the given bearer token.
func (c *Client) Fetch(ctx context.Context, token string) (Stats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+usagePath, nil)
	if err != nil {
		return Stats{}, fmt.Errorf("%w: build request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.doer.Do(req)
	if err != nil {
		return Stats{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Stats{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	var s Stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Stats{}, fmt.Errorf("%w: malformed response: %v", ErrUnavailable, err)
	}
	return s, nil
}
