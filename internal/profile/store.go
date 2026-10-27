// Package profile manages the switcher's own state: the list of named account
// profiles, which one is active, and the on-disk snapshots of Claude Desktop's
// data directory that back each profile.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Profile is one captured Claude account.
type Profile struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
	// Color is a swatch palette index plus one; 0 means "by position".
	Color int `json:"color,omitempty"`
}

// ColorIndex is the palette index for this profile, which sits at position pos
// in the list.
func (p Profile) ColorIndex(pos int) int {
	if p.Color > 0 {
		return p.Color - 1
	}
	return pos
}

// ModeSwitch is one Claude window; switching accounts quits and relaunches it.
// ModeParallel runs each account in its own window via --user-data-dir.
const (
	ModeSwitch   = "switch"
	ModeParallel = "parallel"
)

// Config is the switcher's persisted state.
type Config struct {
	ActiveProfile string    `json:"activeProfile"`
	Profiles      []Profile `json:"profiles"`
	Mode          string    `json:"mode,omitempty"`
	LaunchAtLogin bool      `json:"launchAtLogin,omitempty"`
	// LaunchAtLoginAsked marks that the user has already been asked (or has
	// used the Settings checkbox) about opening at login, so the one-time
	// first-run prompt does not nag on every later launch.
	LaunchAtLoginAsked bool `json:"launchAtLoginAsked,omitempty"`
	// WarnAt is the session or weekly percent that sends one notification
	// until that window resets. Nil uses the default of 80. A pointer to 0
	// turns the alerts off.
	WarnAt *int `json:"warnAt,omitempty"`
	// ShowMultipleProfiles draws every profile as its own compact swatch in
	// the tray icon, instead of a single icon for the app as a whole.
	ShowMultipleProfiles bool `json:"showMultipleProfiles,omitempty"`
	// AutoSwitchOnLimit, when set, switches away from the active profile the
	// moment its session usage reaches 100%, picking whichever other profile
	// has the most room left.
	AutoSwitchOnLimit bool `json:"autoSwitchOnLimit,omitempty"`
	// AutoSwitchAt is the session percent that triggers the auto-switch.
	// 0 means 100; see AutoSwitchPercent.
	AutoSwitchAt int `json:"autoSwitchAt,omitempty"`
	// SwitchCLI makes each account remember its own Claude Code (CLI) login,
	// restored on every switch. Off by default: it moves credentials around.
	SwitchCLI bool `json:"switchCLI,omitempty"`
	// LastSummary is when the weekly usage summary last went out (or, on the
	// first run, when counting started).
	LastSummary time.Time `json:"lastSummary,omitempty"`
	// AutoCheckUpdates controls the daily background check against GitHub's
	// latest release. Nil (never set) defaults to on — see
	// AutoCheckUpdatesEnabled.
	AutoCheckUpdates *bool `json:"autoCheckUpdates,omitempty"`
	// LastUpdateCheck is when a GitHub release check last completed
	// (successfully or not), so the tray's update row and the daily
	// background loop both know whether one is due.
	LastUpdateCheck time.Time `json:"lastUpdateCheck,omitempty"`
	// LatestVersion/LatestVersionURL cache the last known GitHub release, so
	// the tray menu has something to show the moment it opens rather than
	// waiting on a network call.
	LatestVersion    string `json:"latestVersion,omitempty"`
	LatestVersionURL string `json:"latestVersionURL,omitempty"`
}

// AutoCheckUpdatesEnabled reports whether the daily background update check
// is on. Defaults to true — the common case for a small tray app is to let it
// quietly tell you when it's out of date, same as the reference "Automatic
// Updates" checkbox this mirrors.
func (c *Config) AutoCheckUpdatesEnabled() bool {
	return c == nil || c.AutoCheckUpdates == nil || *c.AutoCheckUpdates
}

// DefaultWarnPercent is the limit alert used when WarnAt has never been set.
const DefaultWarnPercent = 80

// WarnPercent returns the configured alert threshold. Values outside 0–100
// are clamped. 0 means alerts are off.
func (c *Config) WarnPercent() int {
	if c == nil || c.WarnAt == nil {
		return DefaultWarnPercent
	}
	n := *c.WarnAt
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// AutoSwitchPercent is the session percent at which auto-switch fires,
// defaulting to 100 and never below 50.
func (c *Config) AutoSwitchPercent() int {
	if c == nil || c.AutoSwitchAt < 50 || c.AutoSwitchAt > 100 {
		return 100
	}
	return c.AutoSwitchAt
}

// IsParallel reports whether side-by-side windows are the current mode.
// An empty mode is the original switch behavior.
func (c *Config) IsParallel() bool { return c.Mode == ModeParallel }

// Store reads and writes the switcher config plus the profile snapshot tree,
// all rooted at the switcher's own config directory.
type Store struct {
	root string // e.g. ~/.config/cd-switcher  (or OS equivalent)
}

// NewStore returns a Store rooted at <UserConfigDir>/cd-switcher, creating the
// directory tree if needed.
func NewStore() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(filepath.Join(base, "cd-switcher"))
}

// NewStoreAt returns a Store rooted at an explicit directory, creating the
// profiles tree if needed. Useful for tests and for relocating switcher state.
func NewStoreAt(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) configPath() string { return filepath.Join(s.root, "config.json") }

// Root is the switcher's config directory, used for the log file and snapshots.
func (s *Store) Root() string { return s.root }

// HistoryPath is where a profile's usage chart is stored, outside the snapshot
// directory so capturing or running Claude against that directory cannot
// overwrite it.
func (s *Store) HistoryPath(id string) string {
	return filepath.Join(s.root, "history", id+".json")
}

// DailyPath is where a profile's per-day usage peaks are stored, alongside
// the history file for the same reason.
func (s *Store) DailyPath(id string) string {
	return filepath.Join(s.root, "daily", id+".json")
}

// ProfileDir returns the snapshot directory for a profile id.
func (s *Store) ProfileDir(id string) string {
	return filepath.Join(s.root, "profiles", id)
}

// RemoveSnapshot deletes a profile's on-disk snapshot. It is a no-op if the
// snapshot does not exist.
func (s *Store) RemoveSnapshot(id string) error {
	return os.RemoveAll(s.ProfileDir(id))
}

// Load reads config.json, returning an empty Config if it does not exist yet.
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// Save writes config.json atomically (temp file + rename).
func (s *Store) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.configPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.configPath())
}

// Find returns the profile with the given id, or false.
func (c *Config) Find(id string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Active returns the active profile, or false if none is set / found.
func (c *Config) Active() (Profile, bool) {
	if c.ActiveProfile == "" {
		return Profile{}, false
	}
	return c.Find(c.ActiveProfile)
}

// SetColor gives a profile a palette index, or -1 to go back to by-position.
func (c *Config) SetColor(id string, index int) bool {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			c.Profiles[i].Color = index + 1
			return true
		}
	}
	return false
}

// Rename changes a profile's display label. The id (and its snapshot directory)
// is left unchanged. Returns false if no profile has that id.
func (c *Config) Rename(id, label string) bool {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			c.Profiles[i].Label = label
			return true
		}
	}
	return false
}

// Remove drops a profile from the config. If it was the active profile,
// ActiveProfile is cleared (the live Claude data is not touched — the user just
// stops tracking that account). Returns false if no profile has that id.
func (c *Config) Remove(id string) bool {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			c.Profiles = append(c.Profiles[:i], c.Profiles[i+1:]...)
			if c.ActiveProfile == id {
				c.ActiveProfile = ""
			}
			return true
		}
	}
	return false
}

// Move shifts a profile one step earlier (delta -1) or later (delta +1) in
// the menu order. Returns false if the id is missing or the move would pass
// either end.
func (c *Config) Move(id string, delta int) bool {
	if delta != -1 && delta != 1 {
		return false
	}
	for i := range c.Profiles {
		if c.Profiles[i].ID != id {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(c.Profiles) {
			return false
		}
		c.Profiles[i], c.Profiles[j] = c.Profiles[j], c.Profiles[i]
		return true
	}
	return false
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// NewID derives a unique, filesystem-safe id from a human label, disambiguating
// against ids already present in the config.
func (c *Config) NewID(label string) string {
	base := slugInvalid.ReplaceAllString(strings.ToLower(label), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "account"
	}
	id := base
	for i := 2; ; i++ {
		if _, exists := c.Find(id); !exists {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}
