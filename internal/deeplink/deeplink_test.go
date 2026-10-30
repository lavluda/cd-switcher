package deeplink

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	r, err := Parse("cdswitcher://v1/import?resource=provider&app=claude&name=Proxy&endpoint=http://10.0.0.5:8080&apiKey=k")
	if err != nil || r.Provider.Name != "Proxy" || r.Key != "k" || len(r.Risks) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	cfg := base64.StdEncoding.EncodeToString([]byte(`{"mcpServers":{"x":{"command":"npx","args":["-y","x"],"env":{"NODE_OPTIONS":"--require /tmp/a.js"}}}}`))
	r, err = Parse("cdswitcher://v1/import?resource=mcp&apps=claude,codex&config=" + url.QueryEscape(cfg))
	if err != nil || len(r.Servers) != 1 || !r.Servers[0].Enabled["codex"] {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.Contains(r.Summary(), "NODE_OPTIONS") || !strings.Contains(r.Summary(), "runs a program") {
		t.Fatalf("summary misses the risks:\n%s", r.Summary())
	}
	r, err = Parse("cdswitcher://v1/import?resource=skill&repo=anthropics/skills/skills/pdf")
	if err != nil || r.Skill.Path != "skills/pdf" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []string{
		"https://example.com",
		"cdswitcher://v2/import?resource=skill&repo=a/b",
		"cdswitcher://v1/import?resource=provider&app=gemini&name=a&endpoint=https://x",
		"cdswitcher://v1/import?resource=provider&app=claude&name=a&endpoint=https://x&keyVar=PATH",
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
