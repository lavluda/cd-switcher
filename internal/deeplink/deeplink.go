// Package deeplink reads cdswitcher://v1/import links, which hand the app a
// provider, MCP servers or a skill to add. Nothing is applied from a link
// without the user confirming it, and Risks lists what deserves a second look.
//
//	cdswitcher://v1/import?resource=provider&app=claude&name=..&endpoint=..&model=..&apiKey=..
//	cdswitcher://v1/import?resource=mcp&apps=claude,codex&config=<base64 {"mcpServers":{...}}>
//	cdswitcher://v1/import?resource=skill&repo=owner/repo/path
package deeplink

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/provider"
	"github.com/lavluda/cd-switcher/internal/skills"
)

// Scheme is the URL scheme the app registers.
const Scheme = "cdswitcher"

// Request is a parsed link.
type Request struct {
	Kind     string // "provider", "mcp" or "skill"
	Provider provider.Provider
	Key      string
	Servers  []mcp.Server
	Skill    skills.Source
	Risks    []string
}

// Summary describes the request for the confirmation dialog.
func (r Request) Summary() string {
	var b strings.Builder
	switch r.Kind {
	case "provider":
		fmt.Fprintf(&b, "Add the %s provider “%s”\nEndpoint: %s\n", harness.Title(r.Provider.Harness), r.Provider.Name, r.Provider.BaseURL)
		if r.Provider.Model != "" {
			fmt.Fprintf(&b, "Model: %s\n", r.Provider.Model)
		}
		if r.Key != "" {
			b.WriteString("Includes an API key\n")
		}
	case "mcp":
		for _, s := range r.Servers {
			if s.Remote() {
				fmt.Fprintf(&b, "Add MCP server “%s”: %s\n", s.Name, s.URL)
			} else {
				fmt.Fprintf(&b, "Add MCP server “%s”: runs %s %s\n", s.Name, s.Command, strings.Join(s.Args, " "))
			}
		}
	case "skill":
		fmt.Fprintf(&b, "Install skills from github.com/%s\n", r.Skill)
	}
	if len(r.Risks) > 0 {
		b.WriteString("\nCheck before you continue:\n")
		for _, x := range r.Risks {
			b.WriteString("• " + x + "\n")
		}
	}
	return b.String()
}

// Parse reads a cdswitcher:// link.
func Parse(raw string) (Request, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != Scheme {
		return Request{}, errors.New("not a cdswitcher:// link")
	}
	if u.Host != "v1" || strings.Trim(u.Path, "/") != "import" {
		return Request{}, fmt.Errorf("unsupported link %s://%s%s", u.Scheme, u.Host, u.Path)
	}
	q := u.Query()
	r := Request{Kind: q.Get("resource")}
	switch r.Kind {
	case "provider":
		app := q.Get("app")
		if app != harness.Claude && app != harness.Codex {
			return Request{}, errors.New("provider links need app=claude or app=codex")
		}
		r.Provider = provider.Provider{Name: q.Get("name"), Harness: app, BaseURL: q.Get("endpoint"), Model: q.Get("model"), KeyVar: q.Get("keyVar"), WireAPI: q.Get("wireApi")}
		r.Key = q.Get("apiKey")
		if r.Provider.Name == "" || r.Provider.BaseURL == "" {
			return Request{}, errors.New("provider links need a name and an endpoint")
		}
		if kv := r.Provider.KeyVar; kv != "" && kv != "ANTHROPIC_AUTH_TOKEN" && kv != "ANTHROPIC_API_KEY" {
			return Request{}, errors.New("keyVar must be ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY")
		}
		r.Risks = endpointRisks(r.Provider.BaseURL)
		if r.Key != "" {
			r.Risks = append(r.Risks, "the link carries an API key; make sure you trust where it came from")
		}
	case "mcp":
		b, err := decodeB64(q.Get("config"))
		if err != nil {
			return Request{}, errors.New("mcp links need config as base64 JSON")
		}
		if r.Servers, err = mcp.ParsePasted(string(b)); err != nil {
			return Request{}, err
		}
		apps := strings.Split(q.Get("apps"), ",")
		for i := range r.Servers {
			s := &r.Servers[i]
			s.Enabled = map[string]bool{}
			for _, a := range apps {
				if a = strings.TrimSpace(a); a != "" {
					s.Enabled[a] = true
				}
			}
			if len(s.Enabled) == 0 {
				s.Enabled[harness.Claude] = true
			}
			if s.Remote() {
				r.Risks = append(r.Risks, endpointRisks(s.URL)...)
			} else {
				r.Risks = append(r.Risks, fmt.Sprintf("“%s” runs a program on this computer: %s %s", s.Name, s.Command, strings.Join(s.Args, " ")))
			}
			for k := range s.Env {
				if loaderVar(k) {
					r.Risks = append(r.Risks, fmt.Sprintf("“%s” sets %s, which changes how programs load", s.Name, k))
				}
			}
		}
	case "skill":
		src, err := skills.ParseSource(q.Get("repo"))
		if err != nil {
			return Request{}, err
		}
		r.Skill = src
		r.Risks = append(r.Risks, "skills can include scripts your AI tools may run")
	default:
		return Request{}, errors.New("resource must be provider, mcp or skill")
	}
	return r, nil
}

func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("bad base64")
}

func loaderVar(k string) bool {
	k = strings.ToUpper(k)
	return strings.HasPrefix(k, "DYLD_") || strings.HasPrefix(k, "LD_") || k == "NODE_OPTIONS" || k == "PYTHONPATH" || k == "PATH"
}

func endpointRisks(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return []string{"the endpoint is not a valid URL: " + raw}
	}
	var out []string
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || strings.HasSuffix(host, ".local") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()))
	if local {
		out = append(out, "the endpoint is on this computer or your local network: "+host)
	}
	if u.Scheme != "https" && !local {
		out = append(out, "the endpoint is not HTTPS, so traffic and keys travel unencrypted")
	}
	return out
}
