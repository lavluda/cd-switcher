// Package cctoggles flips a few Claude Code settings people commonly want,
// in ~/.claude/settings.json. The keys match what cc-switch writes.
package cctoggles

import (
	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Toggle is one switch.
type Toggle struct {
	ID    string
	Title string
	Desc  string
	env   string // the env variable it sets, or "" for the attribution toggle
	value string
}

// All lists the toggles in display order.
var All = []Toggle{
	{ID: "attribution", Title: "Hide Claude attribution", Desc: "No co-author line in commits and no Claude footer in pull requests."},
	{ID: "toolsearch", Title: "Enable tool search", Desc: "Load MCP tools on demand instead of all at once.", env: "ENABLE_TOOL_SEARCH", value: "true"},
	{ID: "noupdate", Title: "Turn off auto-update", Desc: "Claude Code stops updating itself.", env: "DISABLE_AUTOUPDATER", value: "1"},
	{ID: "noartifact", Title: "Turn off the artifact tool", Desc: "", env: "CLAUDE_CODE_DISABLE_ARTIFACT", value: "1"},
}

// State reports which toggles are on.
func State() map[string]bool {
	on := map[string]bool{}
	doc, err := cfgfile.ReadJSON(harness.ClaudeSettings())
	if err != nil {
		return on
	}
	env, _ := doc.Child("env")
	for _, t := range All {
		if t.env == "" {
			var a struct {
				Commit     *string `json:"commit"`
				PR         *string `json:"pr"`
				SessionURL *bool   `json:"sessionUrl"`
			}
			_ = doc.Get("attribution", &a)
			on[t.ID] = a.Commit != nil && *a.Commit == "" && a.PR != nil && *a.PR == ""
			continue
		}
		var v string
		if env != nil {
			_ = env.Get(t.env, &v)
		}
		on[t.ID] = v == t.value
	}
	return on
}

// Set turns one toggle on or off.
func Set(id string, on bool) error {
	return cfgfile.UpdateJSON(harness.ClaudeSettings(), 0, false, func(doc *cfgfile.Object) (bool, error) {
		for _, t := range All {
			if t.ID != id {
				continue
			}
			if t.env == "" {
				if !on {
					return doc.Delete("attribution"), nil
				}
				a := cfgfile.NewObject()
				a.Set("commit", "")
				a.Set("pr", "")
				a.Set("sessionUrl", false)
				return true, doc.Set("attribution", a)
			}
			env, err := doc.Child("env")
			if err != nil {
				return false, err
			}
			if on {
				env.Set(t.env, t.value)
			} else if !env.Delete(t.env) {
				return false, nil
			}
			if env.Len() == 0 {
				doc.Delete("env")
				return true, nil
			}
			return true, doc.Set("env", env)
		}
		return false, nil
	})
}
