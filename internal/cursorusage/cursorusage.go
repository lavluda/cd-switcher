// Package cursorusage reads the Cursor login already stored on this machine
// and asks Cursor for the account's usage history. Cursor does not write a
// token log locally the way Claude Code and Codex do. The login is sent only
// to Cursor.
package cursorusage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

// ErrNoLogin means Cursor has no saved login on this machine.
var ErrNoLogin = errors.New("no Cursor login on this machine")

// Event is one billed request from Cursor's usage history.
type Event struct {
	At         time.Time
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64 // dollars, as Cursor billed it
}

var (
	eventsURL  = "https://cursor.com/api/dashboard/get-filtered-usage-events"
	periodURL  = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage"
	httpClient = &http.Client{Timeout: 20 * time.Second}
)

// Period is the included-usage allowance Cursor shows for the current cycle.
type Period struct {
	Percent float64
	Resets  time.Time
}

// Current reads the signed-in account's included-usage percent.
func Current(ctx context.Context) (Period, error) {
	token, err := readToken()
	if err != nil {
		return Period{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, periodURL, strings.NewReader("{}"))
	if err != nil {
		return Period{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-cursor-client-type", "ide")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Period{}, err
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Period{}, ErrNoLogin
	}
	if resp.StatusCode != http.StatusOK {
		return Period{}, fmt.Errorf("cursor usage: %s", resp.Status)
	}
	var body struct {
		End  string `json:"billingCycleEnd"`
		Plan struct {
			Percent float64 `json:"totalPercentUsed"`
		} `json:"planUsage"`
	}
	if json.Unmarshal(b, &body) != nil {
		return Period{}, errors.New("cursor usage: unexpected response")
	}
	ms, err := strconv.ParseInt(body.End, 10, 64)
	if err != nil {
		return Period{}, errors.New("cursor usage: unexpected response")
	}
	return Period{Percent: body.Plan.Percent, Resets: time.UnixMilli(ms)}, nil
}

// readToken is replaced in tests.
var readToken = accessToken

// Events returns the account's usage between from and to.
func Events(ctx context.Context, from, to time.Time) ([]Event, error) {
	token, err := readToken()
	if err != nil {
		return nil, err
	}
	return eventsWithToken(ctx, token, from, to)
}

func eventsWithToken(ctx context.Context, token string, from, to time.Time) ([]Event, error) {
	user, err := userID(token)
	if err != nil {
		return nil, err
	}
	var out []Event
	for page := 1; page <= 15; page++ {
		body, _ := json.Marshal(map[string]any{
			"startDate": strconv.FormatInt(from.UnixMilli(), 10),
			"endDate":   strconv.FormatInt(to.UnixMilli(), 10),
			"page":      page,
			"pageSize":  1000,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://cursor.com")
		req.Header.Set("Cookie", "WorkosCursorSessionToken="+user+"%3A%3A"+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrNoLogin
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("cursor usage: %s", resp.Status)
		}
		var pageBody struct {
			Total  int        `json:"totalUsageEventsCount"`
			Events []rawEvent `json:"usageEventsDisplay"`
		}
		if json.Unmarshal(b, &pageBody) != nil {
			return nil, errors.New("cursor usage: unexpected response")
		}
		for _, e := range pageBody.Events {
			if ev, ok := e.event(); ok {
				out = append(out, ev)
			}
		}
		if len(pageBody.Events) < 1000 || len(out) >= pageBody.Total {
			break
		}
	}
	return out, nil
}

type rawEvent struct {
	Timestamp string `json:"timestamp"`
	Model     string `json:"model"`
	Tokens    struct {
		Input      flexInt   `json:"inputTokens"`
		Output     flexInt   `json:"outputTokens"`
		CacheWrite flexInt   `json:"cacheWriteTokens"`
		CacheRead  flexInt   `json:"cacheReadTokens"`
		Cents      flexFloat `json:"totalCents"`
	} `json:"tokenUsage"`
	Charged flexFloat `json:"chargedCents"`
}

func (e rawEvent) event() (Event, bool) {
	ms, err := strconv.ParseInt(e.Timestamp, 10, 64)
	if err != nil || ms == 0 {
		return Event{}, false
	}
	cents := float64(e.Tokens.Cents)
	if cents == 0 {
		cents = float64(e.Charged)
	}
	return Event{
		At:         time.UnixMilli(ms),
		Model:      e.Model,
		Input:      int64(e.Tokens.Input),
		Output:     int64(e.Tokens.Output),
		CacheRead:  int64(e.Tokens.CacheRead),
		CacheWrite: int64(e.Tokens.CacheWrite),
		Cost:       cents / 100,
	}, true
}

// flexFloat accepts the cents Cursor sends as a JSON number or string.
type flexFloat float64

func (n *flexFloat) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		f, _ := strconv.ParseFloat(s, 64)
		*n = flexFloat(f)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = flexFloat(f)
	return nil
}

// flexInt accepts the numbers Cursor sends as JSON numbers or strings.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		i, _ := strconv.ParseFloat(s, 64)
		*n = flexInt(i)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = flexInt(f)
	return nil
}

// userID is the account id inside the saved login token.
func userID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("cursor usage: saved login is not a token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("cursor usage: saved login is not a token")
	}
	var payload struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Sub == "" {
		return "", errors.New("cursor usage: saved login has no account")
	}
	sub := payload.Sub
	if i := strings.LastIndex(sub, "|"); i >= 0 {
		sub = sub[i+1:]
	}
	if sub == "" {
		return "", errors.New("cursor usage: saved login has no account")
	}
	return sub, nil
}

// accessToken reads Cursor's saved login. The database stays read-only.
func accessToken() (string, error) {
	db := stateDB()
	if _, err := os.Stat(db); err != nil {
		return "", ErrNoLogin
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return "", errors.New("cursor usage: sqlite3 is not installed")
	}
	cmd := exec.Command("sqlite3", "-readonly", db, "SELECT value FROM ItemTable WHERE key='cursorAuth/accessToken';")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cursor usage: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", ErrNoLogin
	}
	return token, nil
}

func stateDB() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Cursor", "User", "globalStorage", "state.vscdb")
	case "darwin":
		return filepath.Join(harness.Home(), "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(harness.Home(), ".config")
		}
		return filepath.Join(base, "Cursor", "User", "globalStorage", "state.vscdb")
	}
}
