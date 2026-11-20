// Package mcp keeps one list of MCP servers and writes it into each AI tool's
// own config file, in that tool's format:
//
//	Claude Code     ~/.claude.json             mcpServers
//	Claude Desktop  claude_desktop_config.json mcpServers (local servers only)
//	Codex           ~/.codex/config.toml       [mcp_servers.<name>]
//	Cursor          ~/.cursor/mcp.json         mcpServers
//	OpenCode        opencode.json              mcp
//	Gemini CLI      ~/.gemini/settings.json    mcpServers
//
// Only user-level files are touched; project servers stay the project's. An
// entry that already exists is left as written unless the server was edited
// here, so tool-specific extras (timeouts, trust flags) survive.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Server is one MCP server.
type Server struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Transport is "http" or "sse" for a remote server; empty for stdio.
	Transport string `json:"transport,omitempty"`
	// Enabled says which tools get this server.
	Enabled map[string]bool `json:"enabled,omitempty"`
}

// Remote reports whether the server is reached over HTTP.
func (s Server) Remote() bool { return s.URL != "" }

// Valid checks the fields a tool needs.
func (s Server) Valid() error {
	switch {
	case strings.TrimSpace(s.Name) == "":
		return errors.New("an MCP server needs a name")
	case s.Command == "" && s.URL == "":
		return fmt.Errorf("%s needs a command or a URL", s.Name)
	case s.Command != "" && s.URL != "":
		return fmt.Errorf("%s has both a command and a URL; pick one", s.Name)
	}
	return nil
}

// Tools lists the tools MCP servers can be written to, in display order.
var Tools = []string{harness.Claude, harness.Desktop, harness.Codex, harness.Cursor, harness.OpenCode, harness.Gemini}

// Supports reports whether a tool can take this server from a config file.
// Claude Desktop's file only holds local servers; remote ones are added in
// its Connectors settings.
func Supports(tool string, s Server) bool { return !(tool == harness.Desktop && s.Remote()) }

func jsonPath(tool, desktopDir string) (path, key string) {
	switch tool {
	case harness.Claude:
		return harness.ClaudeJSON(), "mcpServers"
	case harness.Desktop:
		return filepath.Join(desktopDir, "claude_desktop_config.json"), "mcpServers"
	case harness.Cursor:
		return filepath.Join(harness.CursorDir(), "mcp.json"), "mcpServers"
	case harness.OpenCode:
		return harness.OpenCodeConfig(), "mcp"
	case harness.Gemini:
		return filepath.Join(harness.GeminiDir(), "settings.json"), "mcpServers"
	}
	return "", ""
}

func codexPath() string { return filepath.Join(harness.CodexHome(), "config.toml") }

// Import reads every installed tool's servers and merges them by name. The
// first tool that has a server supplies its definition; every tool that has
// it is marked enabled.
func Import(desktopDir string) ([]Server, []error) {
	byName := map[string]*Server{}
	var order []string
	var errs []error
	for _, tool := range Tools {
		if !harness.Installed(tool, desktopDir) {
			continue
		}
		found, err := read(tool, desktopDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", harness.Title(tool), err))
			continue
		}
		for _, s := range found {
			cur, ok := byName[s.Name]
			if !ok {
				s := s
				s.Enabled = map[string]bool{}
				byName[s.Name] = &s
				order = append(order, s.Name)
				cur = &s
			}
			cur.Enabled[tool] = true
		}
	}
	out := make([]Server, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, errs
}

func read(tool, desktopDir string) ([]Server, error) {
	if tool == harness.Codex {
		return readCodex()
	}
	path, key := jsonPath(tool, desktopDir)
	doc, err := cfgfile.ReadJSON(path)
	if err != nil {
		return nil, err
	}
	servers, err := doc.Child(key)
	if err != nil {
		return nil, err
	}
	var out []Server
	for _, name := range servers.Keys() {
		raw, _ := servers.Raw(name)
		s, err := decodeJSON(tool, raw)
		if err != nil {
			continue // an entry in a shape we do not know is left alone
		}
		s.Name = name
		out = append(out, s)
	}
	return out, nil
}

// entry is the union of the JSON shapes the tools use.
type entry struct {
	Type        string            `json:"type,omitempty"`
	Command     json.RawMessage   `json:"command,omitempty"` // string, or an array for OpenCode
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	URL         string            `json:"url,omitempty"`
	HTTPURL     string            `json:"httpUrl,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func decodeJSON(tool string, raw json.RawMessage) (Server, error) {
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Server{}, err
	}
	s := Server{Args: e.Args, Env: e.Env, URL: e.URL, Headers: e.Headers}
	var cmd string
	var cmdList []string
	if json.Unmarshal(e.Command, &cmd) != nil {
		_ = json.Unmarshal(e.Command, &cmdList)
		if len(cmdList) > 0 {
			cmd, s.Args = cmdList[0], cmdList[1:]
		}
	}
	s.Command = cmd
	if tool == harness.OpenCode {
		s.Env = e.Environment
	}
	switch {
	case e.HTTPURL != "":
		s.URL, s.Transport = e.HTTPURL, "http"
	case s.URL != "" && (e.Type == "sse" || tool == harness.Gemini):
		s.Transport = "sse"
	case s.URL != "":
		s.Transport = "http"
	}
	if s.Command == "" && s.URL == "" {
		return Server{}, errors.New("no command or url")
	}
	return s, nil
}

func encodeJSON(tool string, s Server) any {
	if tool == harness.OpenCode {
		if s.Remote() {
			m := map[string]any{"type": "remote", "url": s.URL, "enabled": true}
			if len(s.Headers) > 0 {
				m["headers"] = s.Headers
			}
			return m
		}
		m := map[string]any{"type": "local", "command": append([]string{s.Command}, s.Args...), "enabled": true}
		if len(s.Env) > 0 {
			m["environment"] = s.Env
		}
		return m
	}
	m := map[string]any{}
	if s.Remote() {
		switch tool {
		case harness.Gemini:
			if s.Transport == "sse" {
				m["url"] = s.URL
			} else {
				m["httpUrl"] = s.URL
			}
		case harness.Claude:
			t := s.Transport
			if t == "" {
				t = "http"
			}
			m["type"], m["url"] = t, s.URL
		default:
			m["url"] = s.URL
		}
		if len(s.Headers) > 0 {
			m["headers"] = s.Headers
		}
		return m
	}
	if tool == harness.Claude {
		m["type"] = "stdio"
	}
	m["command"] = s.Command
	m["args"] = nonNil(s.Args)
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
	return m
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func readCodex() ([]Server, error) {
	b, err := os.ReadFile(codexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Servers map[string]struct {
			Command string            `toml:"command"`
			Args    []string          `toml:"args"`
			Env     map[string]string `toml:"env"`
			URL     string            `toml:"url"`
			Headers map[string]string `toml:"http_headers"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(b), &doc); err != nil {
		return nil, fmt.Errorf("config.toml: %w", err)
	}
	names := make([]string, 0, len(doc.Servers))
	for n := range doc.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Server
	for _, n := range names {
		v := doc.Servers[n]
		s := Server{Name: n, Command: v.Command, Args: v.Args, Env: v.Env, URL: v.URL, Headers: v.Headers}
		if s.URL != "" {
			s.Transport = "http"
		}
		if s.Command != "" || s.URL != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func encodeCodex(s Server) string {
	var b strings.Builder
	b.WriteString(cfgfile.TableHeader("mcp_servers", s.Name) + "\n")
	if s.Remote() {
		b.WriteString("url = " + cfgfile.TOMLString(s.URL) + "\n")
		if len(s.Headers) > 0 {
			b.WriteString("http_headers = " + cfgfile.TOMLInline(s.Headers) + "\n")
		}
		return b.String()
	}
	b.WriteString("command = " + cfgfile.TOMLString(s.Command) + "\n")
	b.WriteString("args = " + cfgfile.TOMLStrings(nonNil(s.Args)) + "\n")
	if len(s.Env) > 0 {
		b.WriteString("env = " + cfgfile.TOMLInline(s.Env) + "\n")
	}
	return b.String()
}

// Apply writes servers into every installed tool. A server that is enabled
// for a tool and missing there is added; one that is disabled and present is
// removed; one present and enabled is rewritten only when its name is in
// edited. removed names are deleted from every tool. Each tool is handled on
// its own, so one broken file does not stop the others.
func Apply(desktopDir string, servers []Server, edited, removed map[string]bool) []error {
	var errs []error
	for _, tool := range Tools {
		if !harness.Installed(tool, desktopDir) {
			continue
		}
		var err error
		if tool == harness.Codex {
			err = applyCodex(servers, edited, removed)
		} else {
			err = applyJSON(tool, desktopDir, servers, edited, removed)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", harness.Title(tool), err))
		}
	}
	return errs
}

func applyJSON(tool, desktopDir string, servers []Server, edited, removed map[string]bool) error {
	path, key := jsonPath(tool, desktopDir)
	lock := time.Duration(0)
	if tool == harness.Claude {
		lock = 10 * time.Second // Claude Code's own lock on ~/.claude.json
	}
	return cfgfile.UpdateJSON(path, lock, false, func(doc *cfgfile.Object) (bool, error) {
		cur, err := doc.Child(key)
		if err != nil {
			return false, err
		}
		changed := false
		for name := range removed {
			changed = cur.Delete(name) || changed
		}
		for _, s := range servers {
			want := s.Enabled[tool] && Supports(tool, s)
			has := cur.Has(s.Name)
			switch {
			case want && (!has || edited[s.Name]):
				if err := cur.Set(s.Name, encodeJSON(tool, s)); err != nil {
					return false, err
				}
				changed = true
			case !want && has:
				cur.Delete(s.Name)
				changed = true
			}
		}
		if !changed {
			return false, nil
		}
		return true, doc.Set(key, cur)
	})
}

func applyCodex(servers []Server, edited, removed map[string]bool) error {
	have, err := readCodex()
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, s := range have {
		present[s.Name] = true
	}
	b, err := os.ReadFile(codexPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(b)
	orig := text
	for name := range removed {
		text = cfgfile.SetTOMLTable(text, []string{"mcp_servers", name}, "")
	}
	for _, s := range servers {
		want := s.Enabled[harness.Codex]
		switch {
		case want && (!present[s.Name] || edited[s.Name]):
			text = cfgfile.SetTOMLTable(text, []string{"mcp_servers", s.Name}, encodeCodex(s))
		case !want && present[s.Name]:
			text = cfgfile.SetTOMLTable(text, []string{"mcp_servers", s.Name}, "")
		}
	}
	if text == orig {
		return nil
	}
	return cfgfile.WriteFile(codexPath(), []byte(text), false)
}

// Same reports whether two servers connect the same way.
func Same(a, b Server) bool {
	a.Name, b.Name, a.Enabled, b.Enabled = "", "", nil, nil
	return reflect.DeepEqual(normal(a), normal(b))
}

func normal(s Server) Server {
	if len(s.Args) == 0 {
		s.Args = nil
	}
	if len(s.Env) == 0 {
		s.Env = nil
	}
	if len(s.Headers) == 0 {
		s.Headers = nil
	}
	return s
}

// ParsePasted reads servers from pasted JSON in the common
// {"mcpServers": {...}} shape, or a bare {"name": {...}} map.
func ParsePasted(text string) ([]Server, error) {
	doc, err := cfgfile.ParseObject([]byte(strings.TrimSpace(text)))
	if err != nil {
		return nil, errors.New("paste JSON such as {\"mcpServers\": {\"name\": {\"command\": \"npx\", \"args\": [...]}}}")
	}
	if inner, err := doc.Child("mcpServers"); err == nil && inner.Len() > 0 {
		doc = inner
	}
	var out []Server
	for _, name := range doc.Keys() {
		raw, _ := doc.Raw(name)
		s, err := decodeJSON(harness.Claude, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		s.Name = name
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("no servers in the pasted JSON")
	}
	return out, nil
}

// Templates are common servers offered in the Add menu.
var Templates = []Server{
	{Name: "fetch", Command: "uvx", Args: []string{"mcp-server-fetch"}},
	{Name: "time", Command: "uvx", Args: []string{"mcp-server-time"}},
	{Name: "memory", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"}},
	{Name: "sequential-thinking", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-sequential-thinking"}},
	{Name: "context7", Transport: "http", URL: "https://mcp.context7.com/mcp"},
}
