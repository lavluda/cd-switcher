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
}

// Config is the switcher's persisted state.
type Config struct {
	ActiveProfile string    `json:"activeProfile"`
	Profiles      []Profile `json:"profiles"`
}

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
