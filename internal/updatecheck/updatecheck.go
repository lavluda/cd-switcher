// Package updatecheck looks up the latest published GitHub release for
// CD-Switcher and compares it against the running version. It only ever reads
// GitHub's public release metadata — it does not download or install
// anything; CD-Switcher ships via GitHub releases and a Homebrew cask, both
// of which already have their own update mechanisms.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// releasesURL is GitHub's "latest release" API endpoint for this repo.
const releasesURL = "https://api.github.com/repos/lavluda/cd-switcher/releases/latest"

// Release is a published GitHub release's tag and web page.
type Release struct {
	Version string // e.g. "v0.1.4"
	URL     string // e.g. "https://github.com/lavluda/cd-switcher/releases/tag/v0.1.4"
}

// Latest fetches the most recently published GitHub release.
func Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return Release{}, fmt.Errorf("github releases: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Release{}, err
	}
	return Release{Version: payload.TagName, URL: payload.HTMLURL}, nil
}

// IsNewer reports whether latest is a newer "vX.Y.Z" tag than current,
// comparing major/minor/patch numerically. Returns false — rather than
// guessing — if either string doesn't parse, which includes "dev" (an
// unreleased local build, not a version to compare against).
func IsNewer(current, latest string) bool {
	c, ok1 := parseVersion(current)
	l, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseVersion reads a "v1.2.3" (or "1.2.3", or short "1.2") tag into
// [major, minor, patch], defaulting missing trailing parts to 0.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return out, false
	}
	parts := strings.SplitN(v, ".", 3)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
