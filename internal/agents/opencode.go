package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// pluginMark identifies the file Install writes, so Uninstall never removes
// a plugin the user created.
const pluginMark = "cd-switcher opencode plugin"

func pluginFile() string {
	return filepath.Join(harness.OpenCodeDir(), "plugins", "cd-switcher.js")
}

func openCodeInstalled() bool {
	b, err := os.ReadFile(pluginFile())
	return err == nil && strings.Contains(string(b), pluginMark)
}

func installOpenCode(exe string) error {
	body := []byte(pluginSource(exe))
	path := pluginFile()
	if cur, err := os.ReadFile(path); err != nil || string(cur) != string(body) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return registerPlugin(path)
}

func uninstallOpenCode() error {
	path := pluginFile()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !strings.Contains(string(b), pluginMark) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return unregisterPlugin(path)
}

func registerPlugin(path string) error {
	return cfgfile.UpdateJSON(harness.OpenCodeConfig(), 0, false, func(doc *cfgfile.Object) (bool, error) {
		if doc.Has("plugin") {
			var list []string
			if err := doc.Get("plugin", &list); err != nil {
				return false, nil
			}
			for _, p := range list {
				if p == path {
					return false, nil
				}
			}
			return true, doc.Set("plugin", append(list, path))
		}
		return true, doc.Set("plugin", []string{path})
	})
}

func unregisterPlugin(path string) error {
	return cfgfile.UpdateJSON(harness.OpenCodeConfig(), 0, false, func(doc *cfgfile.Object) (bool, error) {
		var list []string
		if err := doc.Get("plugin", &list); err != nil || len(list) == 0 {
			return false, nil
		}
		kept := list[:0]
		for _, p := range list {
			if p != path {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(list) {
			return false, nil
		}
		if len(kept) == 0 {
			doc.Delete("plugin")
			return true, nil
		}
		return true, doc.Set("plugin", kept)
	})
}

// pluginSource is the OpenCode plugin. OpenCode has no command-hook file, so
// this subscribes to its events and runs the same agent-hook binary.
func pluginSource(exe string) string {
	bin, _ := json.Marshal(exe)
	return `// ` + pluginMark + `
import { spawn } from "node:child_process"

const bin = ` + string(bin) + `

function send(payload) {
  try {
    const child = spawn(bin, ["agent-hook", "opencode"], { detached: true, stdio: ["pipe", "ignore", "ignore"] })
    child.stdin.end(JSON.stringify(payload))
    child.unref()
  } catch {}
}

function onEvent(event) {
  const props = event && event.properties ? event.properties : {}
  const info = props.info || {}
  const sessionID = props.sessionID || info.id || ""
  if (!sessionID) return
  const cwd = info.directory || info.path || ""
  const status = props.status && props.status.type
  let name = ""
  if (event.type === "session.status" && status === "busy") name = "UserPromptSubmit"
  else if (event.type === "session.status" && status === "idle") name = "Stop"
  else if (event.type === "session.idle") name = "Stop"
  else if (event.type === "permission.asked") name = "PermissionRequest"
  else if (event.type === "permission.replied") name = "UserPromptSubmit"
  else if (event.type === "session.deleted") name = "SessionEnd"
  else if (event.type === "session.error") name = "StopFailure"
  if (name) send({ hook_event_name: name, session_id: sessionID, cwd })
}

export const CDSwitcher = async () => ({ event: async ({ event }) => onEvent(event) })

export default {
  id: "cd-switcher",
  server: CDSwitcher,
  setup(ctx) {
    const controller = new AbortController()
    ;(async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) onEvent(event)
      } catch {}
    })()
    return () => controller.abort()
  },
}
`
}
