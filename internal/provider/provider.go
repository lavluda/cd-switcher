// Package provider points Claude Code or Codex at a different model API.
//
// Switching writes only the keys a provider owns and leaves everything else in
// the file alone. For Claude Code that is a fixed set of variables in the env
// block of ~/.claude/settings.json, which it reloads live. For Codex it is the
// root model_provider and model keys plus one [model_providers.cdswitcher]
// table in config.toml; Codex reads it on its next start.
//
// The first switch away from the official setup records the values those keys
// had, and switching back to Official puts them back.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Provider is one model API for one tool.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Harness string `json:"harness"` // harness.Claude or harness.Codex
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// KeyVar is the Claude Code variable that carries the key:
	// ANTHROPIC_AUTH_TOKEN (Bearer), ANTHROPIC_API_KEY (x-api-key) or
	// ANTHROPIC_FOUNDRY_API_KEY.
	KeyVar string `json:"keyVar,omitempty"`
	// Env holds extra Claude Code variables, such as the Bedrock switch or the
	// per-tier model names.
	Env map[string]string `json:"env,omitempty"`
	// WireAPI is Codex's wire_api; empty means "responses".
	WireAPI string `json:"wireApi,omitempty"`
}

// claudeKeys are the env variables a Claude provider can own. Switching
// clears all of them first, so nothing from the previous provider leaks into
// the next one.
var claudeKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_CUSTOM_HEADERS",
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"AWS_REGION", "AWS_PROFILE", "CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID",
	"ANTHROPIC_FOUNDRY_RESOURCE", "ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_FOUNDRY_AUTH_TOKEN",
}

// codexTable is the one model provider table CD-Switcher owns in config.toml.
const codexTable = "cdswitcher"

// Saved is what the official setup had before the first switch away from it.
// A nil map or nil pointer means nothing has been recorded yet.
type Saved struct {
	ClaudeEnv map[string]string `json:"claudeEnv,omitempty"`
	// CodexRoot holds the encoded TOML values of model and model_provider;
	// an empty string records that the key was absent.
	CodexRoot map[string]string `json:"codexRoot,omitempty"`
}

// ClaudeEnv returns the variables a provider sets in Claude Code.
func (p Provider) ClaudeEnv(key string) map[string]string {
	env := map[string]string{}
	for k, v := range p.Env {
		env[k] = v
	}
	if p.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = p.BaseURL
	}
	if p.Model != "" {
		env["ANTHROPIC_MODEL"] = p.Model
	}
	if key != "" {
		kv := p.KeyVar
		if kv == "" {
			kv = "ANTHROPIC_AUTH_TOKEN"
		}
		env[kv] = key
	}
	return env
}

// ApplyClaude switches Claude Code to p, or to the official setup when p is
// nil. saved is updated in place the first time the official values are seen.
func ApplyClaude(p *Provider, key string, saved *Saved) error {
	return cfgfile.UpdateJSON(harness.ClaudeSettings(), 0, key != "", func(doc *cfgfile.Object) (bool, error) {
		env, err := doc.Child("env")
		if err != nil {
			return false, err
		}
		if p != nil && saved.ClaudeEnv == nil {
			saved.ClaudeEnv = map[string]string{}
			for _, k := range claudeKeys {
				var v string
				if env.Get(k, &v) == nil && env.Has(k) {
					saved.ClaudeEnv[k] = v
				}
			}
		}
		for _, k := range claudeKeys {
			env.Delete(k)
		}
		for k := range providerEnvKeys(p) {
			env.Delete(k)
		}
		var next map[string]string
		if p != nil {
			next = p.ClaudeEnv(key)
		} else {
			next = saved.ClaudeEnv
			saved.ClaudeEnv = nil
		}
		for _, k := range sortedKeys(next) {
			env.Set(k, next[k])
		}
		if env.Len() == 0 {
			doc.Delete("env")
			return true, nil
		}
		return true, doc.Set("env", env)
	})
}

func providerEnvKeys(p *Provider) map[string]string {
	if p == nil {
		return nil
	}
	return p.Env
}

// ActiveClaude reports which base URL Claude Code is pointed at, "" for the
// official API.
func ActiveClaude() string {
	doc, err := cfgfile.ReadJSON(harness.ClaudeSettings())
	if err != nil {
		return ""
	}
	env, err := doc.Child("env")
	if err != nil {
		return ""
	}
	var u string
	_ = env.Get("ANTHROPIC_BASE_URL", &u)
	return u
}

func codexConfig() string { return filepath.Join(harness.CodexHome(), "config.toml") }

// ApplyCodex switches Codex to p, or back to the official setup when p is
// nil. Codex keeps its ChatGPT login in auth.json, which is never touched, so
// switching back to Official needs no new sign-in.
func ApplyCodex(p *Provider, key string, saved *Saved) error {
	path := codexConfig()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(b)
	if p != nil && saved.CodexRoot == nil {
		saved.CodexRoot = map[string]string{}
		for _, k := range []string{"model", "model_provider"} {
			v, _ := cfgfile.TOMLRootValue(text, k)
			saved.CodexRoot[k] = v
		}
	}
	if p == nil {
		for k, v := range saved.CodexRoot {
			text = cfgfile.SetTOMLRootKey(text, k, v)
		}
		saved.CodexRoot = nil
		text = cfgfile.SetTOMLTable(text, []string{"model_providers", codexTable}, "")
	} else {
		if p.BaseURL == "" {
			return errors.New("a Codex provider needs a base URL")
		}
		wire := p.WireAPI
		if wire == "" {
			wire = "responses"
		}
		var t strings.Builder
		t.WriteString(cfgfile.TableHeader("model_providers", codexTable) + "\n")
		t.WriteString("name = " + cfgfile.TOMLString(p.Name) + "\n")
		t.WriteString("base_url = " + cfgfile.TOMLString(p.BaseURL) + "\n")
		t.WriteString("wire_api = " + cfgfile.TOMLString(wire) + "\n")
		if key != "" {
			t.WriteString("experimental_bearer_token = " + cfgfile.TOMLString(key) + "\n")
		}
		text = cfgfile.SetTOMLTable(text, []string{"model_providers", codexTable}, t.String())
		text = cfgfile.SetTOMLRootKey(text, "model_provider", cfgfile.TOMLString(codexTable))
		if p.Model != "" {
			text = cfgfile.SetTOMLRootKey(text, "model", cfgfile.TOMLString(p.Model))
		}
	}
	return cfgfile.WriteFile(path, []byte(text), true)
}

// ActiveCodex reports whether Codex is pointed at CD-Switcher's provider.
func ActiveCodex() bool {
	b, err := os.ReadFile(codexConfig())
	if err != nil {
		return false
	}
	v, _ := cfgfile.TOMLRootValue(string(b), "model_provider")
	return v == cfgfile.TOMLString(codexTable)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Keys keeps provider keys at rest, owner-only, next to the config.
// shortcut: a 0600 file, the same protection the tools give the key once it is
// written into their own settings; move to the OS keychain if users ask.
type Keys struct{ Path string }

func (k Keys) load() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(k.Path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

// Get returns a provider's key, "" when none is stored.
func (k Keys) Get(id string) string {
	m, _ := k.load()
	return m[id]
}

// Set stores (or, for "", forgets) a provider's key.
func (k Keys) Set(id, key string) error {
	m, err := k.load()
	if err != nil {
		return err
	}
	if key == "" {
		delete(m, id)
	} else {
		m[id] = key
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return cfgfile.WriteFile(k.Path, b, true)
}

// Probe measures how long the endpoint takes to answer, after one warm-up
// request so DNS and TLS setup are not counted. Any HTTP answer counts as
// reachable; it does not check the key or the model.
func Probe(ctx context.Context, url string) (time.Duration, error) {
	if url == "" {
		return 0, errors.New("no base URL")
	}
	c := &http.Client{Timeout: 8 * time.Second}
	get := func() (time.Duration, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		start := time.Now()
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return time.Since(start), nil
	}
	if _, err := get(); err != nil {
		return 0, fmt.Errorf("unreachable: %w", err)
	}
	return get()
}
