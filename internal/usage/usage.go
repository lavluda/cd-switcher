// Package usage calls Claude's undocumented usage-stats endpoint. All
// endpoint knowledge (URL, headers, response shape) lives here so a breaking
// API change is a one-file fix. The endpoint is undocumented and may change
// without notice; callers should poll infrequently and treat any failure as
// "stats unavailable", not a hard error.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// Window is one plan-limit window. Utilization is a 0–100 percentage.
// Used/Limit and the token fields are filled only when the response includes
// them; Claude's documented usage payload usually has the percentage alone.
type Window struct {
	Utilization  float64
	ResetsAt     time.Time
	Used         float64
	Limit        float64
	InputTokens  float64
	OutputTokens float64
}

// ExtraUsage describes pay-as-you-go usage beyond the plan's included limits.
// MonthlyLimit and UsedCredits are minor currency units (cents) — divide by
// 100 for a major-unit amount.
type ExtraUsage struct {
	IsEnabled    bool
	MonthlyLimit float64
	UsedCredits  float64
	Utilization  float64
	Currency     string
}

// Limit is one active or upcoming rate/usage limit. Weekly model-scoped
// entries carry a display name in Scope.
type Limit struct {
	Kind     string
	Group    string
	Percent  float64
	Severity string
	ResetsAt time.Time
	IsActive bool
	Scope    *LimitScope
}

// LimitScope names the model or surface a weekly_scoped limit applies to.
type LimitScope struct {
	Model   *LimitName `json:"model"`
	Surface *LimitName `json:"surface"`
}

// LimitName is a display name inside a limit scope.
type LimitName struct {
	DisplayName string `json:"display_name"`
}

// TokenUse is an absolute token total. Claude's plan-usage response usually
// omits these; they stay zero unless the JSON actually contains them.
type TokenUse struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// Total is input + output + cache read + cache write.
func (t TokenUse) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// Short is a compact total, such as "1.2M", or "" when there is no count.
func (t TokenUse) Short() string {
	if t.Total() == 0 {
		return ""
	}
	return FormatCount(float64(t.Total()))
}

// Detail breaks the total into the parts the response included.
func (t TokenUse) Detail() string {
	if t.Total() == 0 {
		return ""
	}
	var parts []string
	if t.Input > 0 {
		parts = append(parts, FormatCount(float64(t.Input))+" in")
	}
	if t.Output > 0 {
		parts = append(parts, FormatCount(float64(t.Output))+" out")
	}
	if t.CacheRead > 0 {
		parts = append(parts, FormatCount(float64(t.CacheRead))+" cache read")
	}
	if t.CacheWrite > 0 {
		parts = append(parts, FormatCount(float64(t.CacheWrite))+" cache write")
	}
	if len(parts) == 0 {
		return FormatCount(float64(t.Total()))
	}
	return strings.Join(parts, " · ")
}

// Stats is the parsed usage response: the session and weekly windows, any
// model-specific windows, extra spend, and token totals when present.
type Stats struct {
	FiveHour            Window
	SevenDay            Window
	SevenDaySonnet      Window
	SevenDayOpus        Window
	SevenDayCowork      Window
	SevenDayOAuthApps   Window
	SevenDayOmelette    Window
	OmelettePromotional Window
	ExtraUsage          ExtraUsage
	Limits              []Limit
	Tokens              TokenUse
}

// Meter is one labeled limit bar.
type Meter struct {
	Label string
	Window
}

// Meters returns the session and weekly windows, then any model windows the
// response actually included.
func (s Stats) Meters() []Meter {
	var out []Meter
	seen := map[string]bool{}
	add := func(label string, w Window, force bool) {
		key := strings.ToLower(label)
		if seen[key] {
			return
		}
		if !force && !w.present() {
			return
		}
		seen[key] = true
		out = append(out, Meter{Label: label, Window: w})
	}
	add("Session", s.FiveHour, true)
	add("This week", s.SevenDay, true)
	add("Sonnet", s.SevenDaySonnet, false)
	add("Opus", s.SevenDayOpus, false)
	add("Cowork", s.SevenDayCowork, false)
	add("Apps", s.SevenDayOAuthApps, false)
	add("Other", s.SevenDayOmelette, false)
	add("Promo", s.OmelettePromotional, false)
	for _, lim := range s.Limits {
		if lim.Kind != "weekly_scoped" {
			continue
		}
		name := lim.displayName()
		if name == "" {
			continue
		}
		add(name, Window{Utilization: lim.Percent, ResetsAt: lim.ResetsAt}, true)
	}
	return out
}

func (w Window) present() bool {
	return w.Utilization != 0 || !w.ResetsAt.IsZero() || w.Used != 0 || w.Limit != 0 || w.InputTokens != 0 || w.OutputTokens != 0
}

// CountLabel is an absolute count for this window, or "" when the response
// only included a percentage.
func (w Window) CountLabel() string {
	if w.InputTokens+w.OutputTokens > 0 {
		if w.InputTokens > 0 && w.OutputTokens > 0 {
			return FormatCount(w.InputTokens) + " in · " + FormatCount(w.OutputTokens) + " out"
		}
		return FormatCount(w.InputTokens+w.OutputTokens) + " tokens"
	}
	if w.Limit > 0 && w.Limit != w.Utilization {
		return FormatCount(w.Used) + " / " + FormatCount(w.Limit)
	}
	return ""
}

// SpendLabel is the extra-usage line, such as "$1.50 of $20.00".
func (e ExtraUsage) SpendLabel() string {
	if !e.IsEnabled && e.MonthlyLimit <= 0 && e.UsedCredits <= 0 {
		return ""
	}
	if e.MonthlyLimit <= 0 && e.UsedCredits <= 0 {
		return fmt.Sprintf("Extra usage %.0f%%", e.Utilization)
	}
	return fmt.Sprintf("Extra %s of %s", formatMoney(e.UsedCredits, e.Currency), formatMoney(e.MonthlyLimit, e.Currency))
}

func (l Limit) displayName() string {
	if l.Scope == nil {
		return ""
	}
	if l.Scope.Model != nil {
		if name := strings.TrimSpace(l.Scope.Model.DisplayName); name != "" {
			return name
		}
	}
	if l.Scope.Surface != nil {
		return strings.TrimSpace(l.Scope.Surface.DisplayName)
	}
	return ""
}

// FormatCount renders a count as 950, 1.2k, or 3.4M.
func FormatCount(n float64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	switch {
	case n >= 1_000_000_000:
		return sign + trimCount(n/1e9) + "B"
	case n >= 1_000_000:
		return sign + trimCount(n/1e6) + "M"
	case n >= 10_000:
		return fmt.Sprintf("%s%.0fk", sign, n/1e3)
	case n >= 1000:
		return sign + trimCount(n/1e3) + "k"
	default:
		return fmt.Sprintf("%s%.0f", sign, n)
	}
}

func trimCount(n float64) string {
	s := fmt.Sprintf("%.1f", n)
	return strings.TrimSuffix(s, ".0")
}

func formatMoney(minor float64, currency string) string {
	major := minor / 100
	switch strings.ToUpper(currency) {
	case "", "USD":
		return fmt.Sprintf("$%.2f", major)
	case "GBP":
		return fmt.Sprintf("£%.2f", major)
	case "EUR":
		return fmt.Sprintf("€%.2f", major)
	default:
		return fmt.Sprintf("%.2f %s", major, strings.ToUpper(currency))
	}
}

// httpDoer is the seam tests use to inject a fake/httptest transport instead
// of making real network calls.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

var orgIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Client fetches usage stats from Claude's API using an OAuth bearer token.
type Client struct {
	doer    httpDoer
	baseURL string

	mu   sync.Mutex
	orgs map[string]string
}

// NewClient returns a Client pointed at the real Claude API.
func NewClient() *Client {
	return &Client{doer: &http.Client{Timeout: defaultTimeout}, baseURL: defaultBaseURL}
}

// newClientWithBase builds a Client against an arbitrary base URL (tests only).
func newClientWithBase(baseURL string, doer httpDoer) *Client {
	return &Client{doer: doer, baseURL: baseURL}
}

// Fetch loads plan usage. It starts with GET /api/oauth/usage, then asks
// /api/oauth/profile for the organization id and merges
// GET /api/organizations/{id}/usage, which is the payload Claude Desktop uses
// for its plan meter. A failure of the organization call keeps the first
// payload.
func (c *Client) Fetch(ctx context.Context, token string) (Stats, error) {
	stats, err := c.getStats(ctx, token, usagePath)
	if err != nil {
		return Stats{}, err
	}
	org := c.cachedOrg(token)
	if org == "" {
		id, oerr := c.lookupOrg(ctx, token)
		if oerr != nil || id == "" {
			return stats, nil
		}
		org = id
		c.storeOrg(token, id)
	}
	richer, rerr := c.getStats(ctx, token, "/api/organizations/"+org+"/usage")
	if rerr != nil {
		return stats, nil
	}
	return mergeStats(richer, stats), nil
}

func (c *Client) getStats(ctx context.Context, token, path string) (Stats, error) {
	body, err := c.get(ctx, token, path)
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	if err := json.Unmarshal(body, &s); err != nil {
		return Stats{}, fmt.Errorf("%w: malformed response: %v", ErrUnavailable, err)
	}
	return s, nil
}

func (c *Client) get(ctx context.Context, token, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	return body, nil
}

func (c *Client) lookupOrg(ctx context.Context, token string) (string, error) {
	body, err := c.get(ctx, token, "/api/oauth/profile")
	if err != nil {
		return "", err
	}
	var p struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	if json.Unmarshal(body, &p) != nil {
		return "", nil
	}
	if !orgIDPattern.MatchString(p.Organization.UUID) {
		return "", nil
	}
	return p.Organization.UUID, nil
}

func (c *Client) cachedOrg(token string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.orgs[tokenKey(token)]
}

func (c *Client) storeOrg(token, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.orgs == nil {
		c.orgs = map[string]string{}
	}
	c.orgs[tokenKey(token)] = id
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func mergeStats(primary, fallback Stats) Stats {
	primary.FiveHour = pickWindow(primary.FiveHour, fallback.FiveHour)
	primary.SevenDay = pickWindow(primary.SevenDay, fallback.SevenDay)
	primary.SevenDaySonnet = pickWindow(primary.SevenDaySonnet, fallback.SevenDaySonnet)
	primary.SevenDayOpus = pickWindow(primary.SevenDayOpus, fallback.SevenDayOpus)
	primary.SevenDayCowork = pickWindow(primary.SevenDayCowork, fallback.SevenDayCowork)
	primary.SevenDayOAuthApps = pickWindow(primary.SevenDayOAuthApps, fallback.SevenDayOAuthApps)
	primary.SevenDayOmelette = pickWindow(primary.SevenDayOmelette, fallback.SevenDayOmelette)
	primary.OmelettePromotional = pickWindow(primary.OmelettePromotional, fallback.OmelettePromotional)
	if !primary.ExtraUsage.IsEnabled && primary.ExtraUsage.MonthlyLimit <= 0 && primary.ExtraUsage.UsedCredits <= 0 {
		primary.ExtraUsage = fallback.ExtraUsage
	}
	if len(primary.Limits) == 0 {
		primary.Limits = fallback.Limits
	}
	if primary.Tokens.Total() == 0 {
		primary.Tokens = fallback.Tokens
	}
	return primary
}

func pickWindow(primary, fallback Window) Window {
	if primary.present() {
		return primary
	}
	return fallback
}

func (s *Stats) UnmarshalJSON(data []byte) error {
	var raw struct {
		FiveHour            Window     `json:"five_hour"`
		SevenDay            Window     `json:"seven_day"`
		SevenDaySonnet      Window     `json:"seven_day_sonnet"`
		SevenDayOpus        Window     `json:"seven_day_opus"`
		SevenDayCowork      Window     `json:"seven_day_cowork"`
		SevenDayOAuthApps   Window     `json:"seven_day_oauth_apps"`
		SevenDayOmelette    Window     `json:"seven_day_omelette"`
		OmelettePromotional Window     `json:"omelette_promotional"`
		ExtraUsage          ExtraUsage `json:"extra_usage"`
		Limits              []Limit    `json:"limits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.FiveHour = raw.FiveHour
	s.SevenDay = raw.SevenDay
	s.SevenDaySonnet = raw.SevenDaySonnet
	s.SevenDayOpus = raw.SevenDayOpus
	s.SevenDayCowork = raw.SevenDayCowork
	s.SevenDayOAuthApps = raw.SevenDayOAuthApps
	s.SevenDayOmelette = raw.SevenDayOmelette
	s.OmelettePromotional = raw.OmelettePromotional
	s.ExtraUsage = raw.ExtraUsage
	s.Limits = raw.Limits
	s.Tokens = tokensFromJSON(data)
	return nil
}

func (w *Window) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var raw struct {
		Utilization  flexFloat `json:"utilization"`
		ResetsAt     flexTime  `json:"resets_at"`
		Used         flexFloat `json:"used"`
		Limit        flexFloat `json:"limit"`
		InputTokens  flexFloat `json:"input_tokens"`
		OutputTokens flexFloat `json:"output_tokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	w.Utilization = float64(raw.Utilization)
	w.ResetsAt = raw.ResetsAt.Time
	w.Used = float64(raw.Used)
	w.Limit = float64(raw.Limit)
	w.InputTokens = float64(raw.InputTokens)
	w.OutputTokens = float64(raw.OutputTokens)
	return nil
}

func (e *ExtraUsage) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var raw struct {
		IsEnabled    bool      `json:"is_enabled"`
		MonthlyLimit flexFloat `json:"monthly_limit"`
		UsedCredits  flexFloat `json:"used_credits"`
		Utilization  flexFloat `json:"utilization"`
		Currency     string    `json:"currency"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.IsEnabled = raw.IsEnabled
	e.MonthlyLimit = float64(raw.MonthlyLimit)
	e.UsedCredits = float64(raw.UsedCredits)
	e.Utilization = float64(raw.Utilization)
	e.Currency = raw.Currency
	return nil
}

func (l *Limit) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var raw struct {
		Kind     string      `json:"kind"`
		Group    string      `json:"group"`
		Percent  flexFloat   `json:"percent"`
		Severity string      `json:"severity"`
		ResetsAt flexTime    `json:"resets_at"`
		IsActive bool        `json:"is_active"`
		Scope    *LimitScope `json:"scope"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.Kind = raw.Kind
	l.Group = raw.Group
	l.Percent = float64(raw.Percent)
	l.Severity = raw.Severity
	l.ResetsAt = raw.ResetsAt.Time
	l.IsActive = raw.IsActive
	l.Scope = raw.Scope
	return nil
}

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	if f == nil || string(data) == "null" {
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = flexFloat(n)
		return nil
	}
	var n float64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*f = flexFloat(n)
	return nil
}

type flexTime struct{ time.Time }

func (t *flexTime) UnmarshalJSON(data []byte) error {
	if t == nil || string(data) == "null" {
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, s)
		}
		if err != nil {
			return err
		}
		t.Time = parsed
		return nil
	}
	var n float64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	sec, frac := math.Modf(n)
	t.Time = time.Unix(int64(sec), int64(frac*1e9)).UTC()
	return nil
}

func tokensFromJSON(data []byte) TokenUse {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return TokenUse{}
	}
	best := tokenFields(root)
	for _, key := range []string{"usage", "tokens", "spend", "token_usage"} {
		raw, ok := root[key]
		if !ok {
			continue
		}
		var child map[string]json.RawMessage
		if json.Unmarshal(raw, &child) != nil {
			continue
		}
		nested := tokenFields(child)
		if nested.Total() > best.Total() {
			best = nested
		}
	}
	return best
}

func tokenFields(m map[string]json.RawMessage) TokenUse {
	var t TokenUse
	read := func(key string) int64 {
		raw, ok := m[key]
		if !ok {
			return 0
		}
		var n flexFloat
		if json.Unmarshal(raw, &n) != nil {
			return 0
		}
		return int64(n)
	}
	t.Input = read("input_tokens")
	t.Output = read("output_tokens")
	t.CacheRead = read("cache_read_input_tokens")
	t.CacheWrite = read("cache_creation_input_tokens")
	return t
}
