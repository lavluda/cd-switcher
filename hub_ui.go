package main

// Settings tabs for everything beyond Claude Desktop accounts. Like the
// other tabs they are rebuilt on every refresh, so anything the user types
// goes into a dialog, not into a field on the tab itself.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/cctoggles"
	"github.com/lavluda/cd-switcher/internal/copilot"
	"github.com/lavluda/cd-switcher/internal/envcheck"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/instructions"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/provider"
	"github.com/lavluda/cd-switcher/internal/skills"
	"github.com/lavluda/cd-switcher/internal/terminal"
	"github.com/lavluda/cd-switcher/internal/usage"
)

func tabHeader(title, subtitle string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	sub := widget.NewLabel(subtitle)
	sub.Importance = widget.LowImportance
	sub.Wrapping = fyne.TextWrapWord
	head := fyne.CanvasObject(headingText(title))
	if len(actions) > 0 {
		// Buttons sit beside the title, centred, so they keep their own
		// height and the subtitle below gets the full width.
		head = container.NewBorder(nil, nil, nil, container.NewCenter(container.NewHBox(actions...)), head)
	}
	return vbox(2, head, sub)
}

// actionRow is a list row: text on the left, buttons on the right kept at
// their own height instead of stretched to the row's.
func actionRow(left fyne.CanvasObject, right ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, container.NewCenter(container.NewHBox(right...)), left)
}

// mutedText is a one-line, non-wrapping low-importance label for use inside
// an HBox, where a wrapping label gets no width and breaks every character.
func mutedText(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	return l
}

func lowLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	l.Wrapping = fyne.TextWrapWord
	return l
}

func iconButton(icon fyne.Resource, tapped func()) *widget.Button {
	b := widget.NewButtonWithIcon("", icon, tapped)
	b.Importance = widget.LowImportance
	return b
}

func scrollTab(items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVScroll(container.NewPadded(vbox(10, items...)))
}

// showDialog opens d over the Settings window.
func (a *app) showForm(title, confirm string, items []*widget.FormItem, size fyne.Size, done func()) {
	if a.settingsWin == nil {
		return
	}
	d := dialog.NewForm(title, confirm, "Cancel", items, func(ok bool) {
		if ok {
			done()
		}
	}, a.settingsWin)
	d.Resize(size)
	d.Show()
}

// ---------- Codex & Copilot ----------

func (a *app) codexTabContent() fyne.CanvasObject {
	a.mu.Lock()
	accts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	active := a.cfg.ActiveCodex
	a.mu.Unlock()

	addCurrent := widget.NewButtonWithIcon("Save current login", theme.DownloadIcon(), func() { a.runOp(a.doCodexAddCurrent) })
	signIn := widget.NewButtonWithIcon("Sign in…", theme.LoginIcon(), a.startCodexLogin)
	signIn.Importance = widget.HighImportance
	items := []fyne.CanvasObject{tabHeader("Codex & Copilot", "Each Codex account keeps its own ChatGPT login. Switching swaps Codex's auth.json; running codex sessions need a restart.", addCurrent, signIn)}

	if !harness.Installed(harness.Codex, "") {
		items = append(items, newPanel(lowLabel("Codex is not installed on this Mac (no "+harness.CodexHome()+" folder).")))
	} else if len(accts) == 0 {
		items = append(items, newPanel(lowLabel("No Codex accounts yet. Save current login keeps the account Codex uses now; Sign in adds another one through your browser.")))
	}
	for _, x := range accts {
		items = append(items, a.codexCard(x, x.ID == active))
	}

	a.statsMu.Lock()
	cp := a.copilot
	a.statsMu.Unlock()
	if !cp.fetchedAt.IsZero() {
		q := cp.quota
		title := widget.NewLabelWithStyle("GitHub Copilot", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		meta := lowLabel(strings.TrimSpace(q.Login + " · " + q.Plan))
		var meter fyne.CanvasObject = lowLabel("Unlimited premium requests")
		if !q.Unlimited {
			meter = usageMeter(usage.Meter{Label: fmt.Sprintf("Premium requests · %d of %d left", q.Remaining, q.Total), Window: usage.Window{Utilization: q.Percent, ResetsAt: q.ResetsAt}})
		}
		items = append(items, newPanel(vbox(6, title, meta, meter)))
	} else if cp.err != nil && !errors.Is(cp.err, copilot.ErrNoLogin) {
		items = append(items, newPanel(lowLabel("GitHub Copilot: "+cp.err.Error())))
	}
	return scrollTab(items...)
}

func (a *app) codexCard(x profile.CodexAccount, active bool) fyne.CanvasObject {
	name := widget.NewLabelWithStyle(x.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	meta := container.NewHBox()
	if active {
		meta.Add(badge("Active"))
	}
	var info []string
	if !strings.EqualFold(x.Email, x.Label) {
		info = append(info, x.Email)
	}
	if x.Plan != "" {
		info = append(info, x.Plan)
	}
	if len(info) > 0 {
		meta.Add(mutedText(strings.Join(info, " · ")))
	}
	body := []fyne.CanvasObject{name}
	if len(meta.Objects) > 0 {
		body = append(body, meta)
	}

	a.statsMu.Lock()
	e, ok := a.codexCache[x.ID]
	a.statsMu.Unlock()
	switch {
	case ok && !e.fetchedAt.IsZero():
		var cells []fyne.CanvasObject
		if e.quota.Session.Seconds > 0 {
			cells = append(cells, usageMeter(usage.Meter{Label: "Session", Window: usage.Window{Utilization: e.quota.Session.Percent, ResetsAt: e.quota.Session.ResetsAt}}))
		}
		if e.quota.Weekly.Seconds > 0 {
			cells = append(cells, usageMeter(usage.Meter{Label: "This week", Window: usage.Window{Utilization: e.quota.Weekly.Percent, ResetsAt: e.quota.Weekly.ResetsAt}}))
		}
		if len(cells) > 0 {
			body = append(body, container.NewGridWithColumns(2, cells...))
		}
		if e.err != nil {
			body = append(body, lowLabel("Last reading "+e.fetchedAt.Local().Format("3:04pm")+" · "+e.err.Error()))
		}
	case ok && e.err != nil:
		body = append(body, lowLabel(e.err.Error()))
	default:
		body = append(body, lowLabel("Usage will appear after the next refresh."))
	}

	id := x.ID
	actions := container.NewHBox(
		iconButton(theme.DocumentCreateIcon(), func() { a.runOp(func() error { return a.doCodexRename(id) }) }),
		iconButton(theme.DeleteIcon(), func() { a.runOp(func() error { return a.doCodexRemove(id) }) }),
	)
	var use fyne.CanvasObject
	if !active {
		b := widget.NewButton("Use", func() { a.runOp(func() error { return a.doCodexSwitch(id) }) })
		b.Importance = widget.HighImportance
		use = b
	}
	body = append(body, container.NewBorder(nil, nil, actions, use))
	return newPanel(vbox(6, body...))
}

// ---------- providers ----------

func (a *app) providersTabContent() fyne.CanvasObject {
	a.mu.Lock()
	providers := append([]provider.Provider(nil), a.cfg.Providers...)
	active := map[string]string{}
	for k, v := range a.cfg.ActiveProvider {
		active[k] = v
	}
	a.mu.Unlock()

	items := []fyne.CanvasObject{tabHeader("Providers", "Point Claude Code or Codex at another model API. Only the endpoint, key and model settings change; everything else in their config stays as it is.")}

	if found := envcheck.Scan(); len(found) > 0 {
		rows := []fyne.CanvasObject{lowLabel("These variables override what is set here in every terminal that loads them:")}
		for _, f := range found {
			f := f
			row := fyne.CanvasObject(widget.NewLabel(f.String()))
			if f.File != "" && f.Line > 0 {
				row = actionRow(row, widget.NewButton("Comment out", func() {
					a.runOp(func() error { return a.doDisableEnv(f) })
				}))
			}
			rows = append(rows, row)
		}
		items = append(items, sectionCaption("Environment conflicts"), group(rows...))
	}

	for _, tool := range []string{harness.Claude, harness.Codex} {
		tool := tool
		add := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() { a.providerForm(tool, provider.Provider{Harness: tool}) })
		add.Importance = widget.LowImportance
		head := container.NewBorder(nil, nil, sectionCaption(harness.Title(tool)), add)
		rows := []fyne.CanvasObject{a.providerRow(tool, provider.Provider{Name: "Official", Harness: tool}, active[tool] == "")}
		for _, p := range providers {
			if p.Harness == tool {
				rows = append(rows, a.providerRow(tool, p, active[tool] == p.ID))
			}
		}
		items = append(items, head, group(rows...))
	}

	state := cctoggles.State()
	var toggles []fyne.CanvasObject
	for _, t := range cctoggles.All {
		id := t.ID
		toggles = append(toggles, toggleRow(t.Title, t.Desc, state[id], harness.Installed(harness.Claude, ""), func(on bool) {
			a.runOp(func() error { return a.doSetToggle(id, on) })
		}))
	}
	items = append(items, sectionCaption("Claude Code settings"), group(toggles...))
	return scrollTab(items...)
}

func (a *app) providerRow(tool string, p provider.Provider, inUse bool) fyne.CanvasObject {
	name := widget.NewLabelWithStyle(p.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	detail := p.BaseURL
	if p.ID == "" {
		detail = "Your own sign-in"
	}
	if p.Model != "" {
		detail += " · " + p.Model
	}
	left := vbox(0, name, lowLabel(detail))
	var right []fyne.CanvasObject
	if inUse {
		right = append(right, badge("In use"))
	} else {
		id := p.ID
		use := widget.NewButton("Use", func() { a.runOp(func() error { return a.doProviderUse(tool, id) }) })
		right = append(right, use)
	}
	if p.ID != "" {
		p := p
		right = append(right,
			iconButton(theme.MediaPlayIcon(), func() { a.probeProvider(p) }),
			iconButton(theme.DocumentCreateIcon(), func() { a.providerForm(tool, p) }),
			iconButton(theme.DeleteIcon(), func() { a.runOp(func() error { return a.doProviderDelete(p.ID) }) }),
		)
	}
	return actionRow(left, right...)
}

func parseEnvLines(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%q is not NAME=value", l)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func envLines(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + m[k] + "\n")
	}
	return b.String()
}

const (
	keyBearer  = "Bearer token (ANTHROPIC_AUTH_TOKEN)"
	keyAPIKey  = "API key (ANTHROPIC_API_KEY)"
	keyFoundry = "Foundry key (ANTHROPIC_FOUNDRY_API_KEY)"
)

// keyVarChoice and keyVarOf map the form's "Key sent as" choice to the
// variable, in both directions.
var keyVarChoice = map[string]string{"ANTHROPIC_AUTH_TOKEN": keyBearer, "ANTHROPIC_API_KEY": keyAPIKey, "ANTHROPIC_FOUNDRY_API_KEY": keyFoundry}

func keyVarOf(choice string) string {
	for v, c := range keyVarChoice {
		if c == choice {
			return v
		}
	}
	return "ANTHROPIC_AUTH_TOKEN"
}

func keyChoiceOf(v string) string {
	if c, ok := keyVarChoice[v]; ok {
		return c
	}
	return keyBearer
}

func (a *app) providerForm(tool string, p provider.Provider) {
	name := widget.NewEntry()
	name.SetText(p.Name)
	base := widget.NewEntry()
	base.SetText(p.BaseURL)
	base.SetPlaceHolder("https://…")
	model := widget.NewEntry()
	model.SetText(p.Model)
	model.SetPlaceHolder("Leave empty for the tool's default")
	key := widget.NewPasswordEntry()
	if p.ID != "" {
		key.SetPlaceHolder("Leave empty to keep the stored key")
	}
	keyVar := widget.NewSelect([]string{keyBearer, keyAPIKey, keyFoundry}, nil)
	keyVar.SetSelected(keyChoiceOf(p.KeyVar))
	extra := widget.NewMultiLineEntry()
	extra.SetText(envLines(p.Env))
	extra.SetPlaceHolder("ANTHROPIC_DEFAULT_SONNET_MODEL=…")
	extra.SetMinRowsVisible(3)
	wire := widget.NewSelect([]string{"responses", "chat"}, nil)
	wire.SetSelected("responses")
	if p.WireAPI != "" {
		wire.SetSelected(p.WireAPI)
	}

	var presetNames []string
	presets := provider.PresetsFor(tool)
	for _, pr := range presets {
		presetNames = append(presetNames, pr.Name)
	}
	preset := widget.NewSelect(append([]string{"Custom"}, presetNames...), func(choice string) {
		for _, pr := range presets {
			if pr.Name == choice {
				name.SetText(pr.Name)
				base.SetText(pr.BaseURL)
				extra.SetText(envLines(pr.Env))
				keyVar.SetSelected(keyChoiceOf(pr.KeyVar))
			}
		}
	})
	preset.PlaceHolder = "Start from a preset"

	items := []*widget.FormItem{}
	if p.ID == "" {
		items = append(items, widget.NewFormItem("Preset", preset))
	}
	items = append(items,
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Base URL", base),
		widget.NewFormItem("Model", model),
		widget.NewFormItem("API key", key),
	)
	if tool == harness.Claude {
		items = append(items, widget.NewFormItem("Key sent as", keyVar), widget.NewFormItem("More variables", extra))
	} else {
		items = append(items, widget.NewFormItem("Wire API", wire))
	}
	title := "Add " + harness.Title(tool) + " provider"
	if p.ID != "" {
		title = "Edit " + p.Name
	}
	a.showForm(title, "Save", items, fyne.NewSize(520, 0), func() {
		next := p
		next.Name, next.BaseURL, next.Model = strings.TrimSpace(name.Text), strings.TrimSpace(base.Text), strings.TrimSpace(model.Text)
		if tool == harness.Claude {
			env, err := parseEnvLines(extra.Text)
			if err != nil {
				notifyErr("Provider not saved", err)
				return
			}
			next.Env = env
			next.KeyVar = keyVarOf(keyVar.Selected)
		} else {
			next.WireAPI = wire.Selected
		}
		k := key.Text
		a.runOp(func() error { return a.doProviderSave(next, k) })
	})
}

// ---------- MCP ----------

func (a *app) mcpTabContent() fyne.CanvasObject {
	a.mu.Lock()
	servers := append([]mcp.Server(nil), a.cfg.MCPServers...)
	a.mu.Unlock()
	desktopDir := a.desktopDir()
	var tools []string
	for _, t := range mcp.Tools {
		if harness.Installed(t, desktopDir) {
			tools = append(tools, t)
		}
	}

	add := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() { a.mcpForm(mcp.Server{}, "") })
	add.Importance = widget.HighImportance
	paste := widget.NewButton("Paste JSON…", a.mcpPasteForm)
	var templateNames []string
	for _, t := range mcp.Templates {
		templateNames = append(templateNames, t.Name)
	}
	templates := widget.NewSelect(templateNames, func(choice string) {
		for _, t := range mcp.Templates {
			if t.Name == choice {
				a.mcpForm(t, "")
			}
		}
	})
	templates.PlaceHolder = "Templates"
	reimport := iconButton(theme.ViewRefreshIcon(), func() { a.runOp(a.doMCPImport) })

	items := []fyne.CanvasObject{tabHeader("MCP servers", "One list, written into each tool's own config. Changes reach Claude Code and Cursor at their next start, Claude Desktop after a restart.", reimport, templates, paste, add)}
	if len(servers) == 0 {
		items = append(items, newPanel(lowLabel("No MCP servers yet.")))
	}
	for _, s := range servers {
		items = append(items, a.mcpCard(s, tools))
	}
	return scrollTab(items...)
}

func (a *app) mcpCard(s mcp.Server, tools []string) fyne.CanvasObject {
	name := widget.NewLabelWithStyle(s.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	what := strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	if s.Remote() {
		what = s.URL
	}
	checks := container.NewGridWrap(fyne.NewSize(150, 36))
	for _, t := range tools {
		t := t
		c := widget.NewCheck(harness.Title(t), nil)
		c.SetChecked(s.Enabled[t] && mcp.Supports(t, s))
		name := s.Name
		c.OnChanged = func(on bool) { a.runOp(func() error { return a.doMCPToggle(name, t, on) }) }
		if !mcp.Supports(t, s) {
			c.Disable()
		}
		checks.Add(c)
	}
	srv := s
	head := actionRow(vbox(0, name, lowLabel(what)),
		iconButton(theme.DocumentCreateIcon(), func() { a.mcpForm(srv, srv.Name) }),
		iconButton(theme.DeleteIcon(), func() { a.runOp(func() error { return a.doMCPDelete(srv.Name) }) }),
	)
	return newPanel(vbox(4, head, checks))
}

func headerLines(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + ": " + m[k] + "\n")
	}
	return b.String()
}

func (a *app) mcpForm(s mcp.Server, oldName string) {
	const (
		local = "Local command"
		http  = "Remote (HTTP)"
		sse   = "Remote (SSE)"
	)
	name := widget.NewEntry()
	name.SetText(s.Name)
	kind := widget.NewSelect([]string{local, http, sse}, nil)
	switch {
	case s.Remote() && s.Transport == "sse":
		kind.SetSelected(sse)
	case s.Remote():
		kind.SetSelected(http)
	default:
		kind.SetSelected(local)
	}
	command := widget.NewEntry()
	command.SetText(s.Command)
	command.SetPlaceHolder("npx")
	args := widget.NewMultiLineEntry()
	args.SetText(strings.Join(s.Args, "\n"))
	args.SetPlaceHolder("One argument per line")
	args.SetMinRowsVisible(3)
	env := widget.NewMultiLineEntry()
	env.SetText(envLines(s.Env))
	env.SetPlaceHolder("NAME=value")
	env.SetMinRowsVisible(2)
	url := widget.NewEntry()
	url.SetText(s.URL)
	url.SetPlaceHolder("https://…/mcp")
	headers := widget.NewMultiLineEntry()
	headers.SetText(headerLines(s.Headers))
	headers.SetPlaceHolder("Authorization: Bearer …")
	headers.SetMinRowsVisible(2)

	items := []*widget.FormItem{
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Type", kind),
		widget.NewFormItem("Command", command),
		widget.NewFormItem("Arguments", args),
		widget.NewFormItem("Environment", env),
		widget.NewFormItem("URL", url),
		widget.NewFormItem("Headers", headers),
	}
	title := "Add MCP server"
	if oldName != "" {
		title = "Edit " + oldName
	}
	a.showForm(title, "Save", items, fyne.NewSize(560, 0), func() {
		next := mcp.Server{Name: strings.TrimSpace(name.Text), Enabled: s.Enabled}
		if kind.Selected == local {
			next.Command = strings.TrimSpace(command.Text)
			for _, l := range strings.Split(args.Text, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					next.Args = append(next.Args, l)
				}
			}
			e, err := parseEnvLines(env.Text)
			if err != nil {
				notifyErr("MCP server not saved", err)
				return
			}
			if len(e) > 0 {
				next.Env = e
			}
		} else {
			next.URL, next.Transport = strings.TrimSpace(url.Text), "http"
			if kind.Selected == sse {
				next.Transport = "sse"
			}
			for _, l := range strings.Split(headers.Text, "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) != "" {
					if next.Headers == nil {
						next.Headers = map[string]string{}
					}
					next.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
		}
		a.runOp(func() error { return a.doMCPSave(next, oldName) })
	})
}

func (a *app) mcpPasteForm() {
	text := widget.NewMultiLineEntry()
	text.SetPlaceHolder(`{"mcpServers": {"name": {"command": "npx", "args": ["-y", "pkg"]}}}`)
	text.SetMinRowsVisible(10)
	a.showForm("Paste MCP JSON", "Add", []*widget.FormItem{widget.NewFormItem("", text)}, fyne.NewSize(600, 0), func() {
		servers, err := mcp.ParsePasted(text.Text)
		if err != nil {
			notifyErr("Nothing added", err)
			return
		}
		a.runOp(func() error {
			for _, s := range servers {
				old := ""
				if _, ok := a.findMCP(s.Name); ok {
					old = s.Name
				}
				if err := a.doMCPSave(s, old); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// ---------- skills ----------

func (a *app) skillsTabContent() fyne.CanvasObject {
	a.mu.Lock()
	sources := map[string]profile.SkillSource{}
	for k, v := range a.cfg.SkillSources {
		sources[k] = v
	}
	a.mu.Unlock()

	fromGitHub := widget.NewButtonWithIcon("From GitHub…", theme.DownloadIcon(), func() {
		src := widget.NewEntry()
		src.SetPlaceHolder("anthropics/skills/skills/pdf")
		a.showForm("Install skills from GitHub", "Install", []*widget.FormItem{
			widget.NewFormItem("Repository path", src),
		}, fyne.NewSize(520, 0), func() {
			s := src.Text
			a.runOp(func() error { return a.doSkillInstall(s) })
		})
	})
	fromGitHub.Importance = widget.HighImportance
	fromZip := widget.NewButton("From zip…", func() { a.runOp(a.doSkillInstallZip) })
	fromFolder := widget.NewButton("From folder…", func() { a.runOp(a.doSkillInstallFolder) })

	items := []fyne.CanvasObject{tabHeader("Skills", "Installed once in ~/.agents/skills, which Codex, Gemini CLI, Cursor, OpenCode and Qwen Code read, and linked into ~/.claude/skills for Claude Code.", fromFolder, fromZip, fromGitHub)}
	list := skills.List()
	if len(list) == 0 {
		items = append(items, newPanel(lowLabel("No skills installed.")))
	}
	var rows []fyne.CanvasObject
	for _, s := range list {
		s := s
		where := "Every tool"
		switch {
		case s.Shared && !s.Linked:
			where = "Every tool except Claude Code"
		case !s.Shared:
			where = "Claude Code only"
		}
		if src := sources[s.Name].Source; src != "" {
			where += " · github.com/" + src
		}
		var right []fyne.CanvasObject
		if !(s.Shared && s.Linked) {
			right = append(right, widget.NewButton("Share with every tool", func() { a.runOp(func() error { return a.doSkillShare(s.Name) }) }))
		}
		if sources[s.Name].Source != "" {
			right = append(right, iconButton(theme.ViewRefreshIcon(), func() { a.runOp(func() error { return a.doSkillUpdate(s.Name) }) }))
		}
		right = append(right, iconButton(theme.DeleteIcon(), func() { a.runOp(func() error { return a.doSkillRemove(s.Name) }) }))
		name := widget.NewLabelWithStyle(s.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		rows = append(rows, actionRow(vbox(0, name, lowLabel(where)), right...))
	}
	if len(rows) > 0 {
		items = append(items, group(rows...))
	}
	return scrollTab(items...)
}

// ---------- instructions ----------

func (a *app) instructionsTabContent() fyne.CanvasObject {
	a.mu.Lock()
	entries := append([]instructions.Entry(nil), a.cfg.Instructions...)
	activeID := a.cfg.ActiveInstructions
	tools := map[string]bool{}
	for k, v := range a.cfg.InstructionTools {
		tools[k] = v
	}
	a.mu.Unlock()

	newBtn := widget.NewButtonWithIcon("New", theme.ContentAddIcon(), func() { a.instructionsForm(instructions.Entry{}) })
	newBtn.Importance = widget.HighImportance
	items := []fyne.CanvasObject{tabHeader("Instructions", "One set of global instructions written to each tool's own file. Project files are left alone.", newBtn)}

	var activeBody string
	for _, e := range entries {
		if e.ID == activeID {
			activeBody = e.Body
		}
	}
	sync := instructions.InSync(activeBody)
	var toolRows []fyne.CanvasObject
	for _, t := range instructions.Targets() {
		t := t
		desc := t.Path
		if activeID != "" && tools[t.Tool] && !sync[t.Tool] {
			desc += " · changed outside CD-Switcher"
		}
		toolRows = append(toolRows, toggleRow(harness.Title(t.Tool), desc, tools[t.Tool], true, func(on bool) {
			a.runOp(func() error { return a.doInstrSetTool(t.Tool, on) })
		}))
	}
	if len(toolRows) > 0 {
		items = append(items, sectionCaption("Write to"), group(toolRows...))
	}

	var rows []fyne.CanvasObject
	for _, e := range entries {
		e := e
		preview := strings.TrimSpace(e.Body)
		if i := strings.IndexByte(preview, '\n'); i >= 0 {
			preview = preview[:i]
		}
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		var right []fyne.CanvasObject
		if e.ID == activeID {
			right = append(right, badge("In use"))
		} else {
			right = append(right, widget.NewButton("Use", func() { a.runOp(func() error { return a.doInstrApply(e.ID) }) }))
		}
		right = append(right,
			iconButton(theme.DocumentCreateIcon(), func() { a.instructionsForm(e) }),
			iconButton(theme.DeleteIcon(), func() { a.runOp(func() error { return a.doInstrDelete(e.ID) }) }),
		)
		name := widget.NewLabelWithStyle(e.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		rows = append(rows, actionRow(vbox(0, name, lowLabel(preview)), right...))
	}
	if len(rows) == 0 {
		rows = append(rows, lowLabel("No instructions saved yet."))
	}
	items = append(items, sectionCaption("Saved"), group(rows...))
	return scrollTab(items...)
}

func (a *app) instructionsForm(e instructions.Entry) {
	name := widget.NewEntry()
	name.SetText(e.Name)
	body := widget.NewMultiLineEntry()
	body.SetText(e.Body)
	body.SetMinRowsVisible(16)
	body.Wrapping = fyne.TextWrapWord
	title := "New instructions"
	if e.ID != "" {
		title = "Edit " + e.Name
	}
	a.showForm(title, "Save", []*widget.FormItem{
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Text", body),
	}, fyne.NewSize(680, 520), func() {
		next := instructions.Entry{ID: e.ID, Name: strings.TrimSpace(name.Text), Body: body.Text}
		a.runOp(func() error { return a.doInstrSave(next) })
	})
}

// ---------- General tab additions ----------

func (a *app) terminalRow(current string) fyne.CanvasObject {
	apps := terminal.Apps()
	if current == "" {
		current = apps[0]
	}
	var suppress bool
	sel := widget.NewSelect(apps, func(v string) {
		if !suppress {
			a.runOp(func() error { return a.doSetTerminal(v) })
		}
	})
	suppress = true
	sel.SetSelected(current)
	suppress = false
	label := widget.NewLabelWithStyle("Terminal", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return vbox(6, container.NewBorder(nil, nil, label, sel), lowLabel("Used by Open Claude Code and Open Codex in the menu."))
}

func (a *app) exportRow() fyne.CanvasObject {
	exp := widget.NewButtonWithIcon("Export…", theme.UploadIcon(), func() { a.runOp(a.doExport) })
	imp := widget.NewButtonWithIcon("Import…", theme.DownloadIcon(), func() { a.runOp(a.doImport) })
	label := widget.NewLabelWithStyle("Setup file", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return vbox(6, container.NewBorder(nil, nil, label, container.NewHBox(exp, imp)),
		lowLabel("Providers, MCP servers, skills and instructions, to move to another Mac. Accounts and API keys stay here."))
}

// ---------- Manage Profiles additions ----------

func (a *app) strategyRow(current string, enabled bool) fyne.CanvasObject {
	const (
		most = "Most room left"
		soon = "Weekly limit that resets soonest"
	)
	var suppress bool
	sel := widget.NewSelect([]string{most, soon}, func(v string) {
		if suppress {
			return
		}
		s := profile.StrategyMostRoom
		if v == soon {
			s = profile.StrategyConsumeFirst
		}
		a.runOp(func() error { return a.doSetStrategy(s) })
	})
	suppress = true
	sel.SetSelected(most)
	if current == profile.StrategyConsumeFirst {
		sel.SetSelected(soon)
	}
	suppress = false
	if !enabled {
		sel.Disable()
	}
	return vbox(6, container.NewBorder(nil, nil, widget.NewLabel("Switch to the account with"), sel),
		lowLabel("Soonest reset spends weekly allowance that would otherwise expire unused. Accounts set to “Skip in auto-switch” are never chosen."))
}

func (a *app) statuslineRow(on bool) fyne.CanvasObject {
	return toggleRow(
		"Read limits from Claude Code",
		"Claude Code reports each account's session and weekly limits to its statusline after every reply. CD-Switcher records them, so meters stay current without polling Anthropic. Your existing statusline keeps running.",
		on, harness.Installed(harness.Claude, ""),
		func(checked bool) { a.runOp(func() error { return a.doSetStatusline(checked) }) },
	)
}

func (a *app) doSetStrategy(s string) error {
	a.cfg.AutoSwitchStrategy = s
	return a.store.Save(a.cfg)
}

func (a *app) doSetNoAutoSwitch(id string, on bool) error {
	for i := range a.cfg.Profiles {
		if a.cfg.Profiles[i].ID == id {
			a.cfg.Profiles[i].NoAutoSwitch = on
		}
	}
	return a.store.Save(a.cfg)
}

// ---------- tray menu additions ----------

// codexMenuItems is the Codex block of the tray menu: each saved account
// with its limits, then the Copilot allowance when there is one.
func (a *app) codexMenuItems() []*fyne.MenuItem {
	a.mu.Lock()
	accts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	active := a.cfg.ActiveCodex
	a.mu.Unlock()
	a.statsMu.Lock()
	cache := map[string]codexEntry{}
	for k, v := range a.codexCache {
		cache[k] = v
	}
	cp := a.copilot
	a.statsMu.Unlock()

	var items []*fyne.MenuItem
	for i, x := range accts {
		if i > 0 {
			items = append(items, fyne.NewMenuItemSeparator())
		}
		id := x.ID
		it := fyne.NewMenuItem("Codex · "+x.Label, func() { a.runOp(func() error { return a.doCodexSwitch(id) }) })
		it.Checked = id == active
		items = append(items, it)
		e, ok := cache[id]
		if !ok || e.fetchedAt.IsZero() {
			items = append(items, dimItem("usage: —"))
			continue
		}
		head := x.Plan
		if e.err != nil {
			head = strings.TrimPrefix(head+" · stale", " · ")
		}
		if head != "" {
			items = append(items, dimItem(head))
		}
		if e.quota.Session.Seconds > 0 {
			items = append(items, gaugeItem(fmt.Sprintf("Session %.0f%% · resets %s", e.quota.Session.Percent, e.quota.Session.ResetsAt.Local().Format("3:04pm")), e.quota.Session.Percent, a.openSettings))
		}
		if e.quota.Weekly.Seconds > 0 {
			items = append(items, gaugeItem(fmt.Sprintf("Weekly %.0f%% · resets %s", e.quota.Weekly.Percent, e.quota.Weekly.ResetsAt.Local().Format("Mon Jan 2")), e.quota.Weekly.Percent, a.openSettings))
		}
	}
	if !cp.fetchedAt.IsZero() && !cp.quota.Unlimited {
		if len(items) > 0 {
			items = append(items, fyne.NewMenuItemSeparator())
		}
		items = append(items, gaugeItem(fmt.Sprintf("Copilot premium %.0f%% · %d left", cp.quota.Percent, cp.quota.Remaining), cp.quota.Percent, a.openSettings))
	}
	return items
}

// toolMenuItems are the provider submenus and the terminal launchers.
func (a *app) toolMenuItems() []*fyne.MenuItem {
	a.mu.Lock()
	providers := append([]provider.Provider(nil), a.cfg.Providers...)
	active := map[string]string{}
	for k, v := range a.cfg.ActiveProvider {
		active[k] = v
	}
	a.mu.Unlock()

	var items []*fyne.MenuItem
	for _, tool := range []string{harness.Claude, harness.Codex} {
		tool := tool
		var children []*fyne.MenuItem
		for _, p := range providers {
			if p.Harness != tool {
				continue
			}
			id := p.ID
			c := fyne.NewMenuItem(p.Name, func() { a.runOp(func() error { return a.doProviderUse(tool, id) }) })
			c.Checked = active[tool] == id
			children = append(children, c)
		}
		if len(children) == 0 {
			continue
		}
		official := fyne.NewMenuItem("Official", func() { a.runOp(func() error { return a.doProviderUse(tool, "") }) })
		official.Checked = active[tool] == ""
		parent := fyne.NewMenuItem(harness.Title(tool)+" provider", nil)
		parent.ChildMenu = fyne.NewMenu("", append([]*fyne.MenuItem{official}, children...)...)
		items = append(items, parent)
	}
	if harness.Installed(harness.Claude, "") {
		it := fyne.NewMenuItem("Open Claude Code", func() { a.openInTerminal("claude") })
		it.Icon = theme.ComputerIcon()
		items = append(items, it)
	}
	if harness.Installed(harness.Codex, "") {
		it := fyne.NewMenuItem("Open Codex", func() { a.openInTerminal("codex") })
		it.Icon = theme.ComputerIcon()
		items = append(items, it)
	}
	return items
}
