package main

// Settings tabs for everything beyond Claude Desktop accounts. Like the
// other tabs they are rebuilt on every refresh, so anything the user types
// goes into a dialog, not into a field on the tab itself.

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lavluda/cd-switcher/internal/cctoggles"
	"github.com/lavluda/cd-switcher/internal/envcheck"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/instructions"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/provider"
	"github.com/lavluda/cd-switcher/internal/skills"
	"github.com/lavluda/cd-switcher/internal/uitheme"
)

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

func scrollTab(items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVScroll(container.NewPadded(vbox(10, items...)))
}

// showForm opens a sheet over the panel: a title, the fields in a
// scrolling body, and Cancel and the confirm button pinned right under a
// hairline, so a long form never hides behind its own buttons.
func (a *app) showForm(title, confirm string, items []*widget.FormItem, size fyne.Size, done func()) {
	win := a.panelWin
	if win == nil {
		return
	}
	// Labels sit above their fields, so long field names never squeeze the
	// inputs and every field gets the sheet's full width.
	var fields []fyne.CanvasObject
	for _, it := range items {
		var parts []fyne.CanvasObject
		if it.Text != "" {
			l := canvas.NewText(it.Text, uitheme.CurrentMuted())
			l.TextSize = 12
			parts = append(parts, container.New(layout.NewCustomPaddedLayout(0, 0, 2, 0), l))
		}
		field := it.Widget
		if _, ok := field.(*widget.Select); ok {
			// A Select has no outline of its own, unlike the entries beside it.
			box := canvas.NewRectangle(color.Transparent)
			box.CornerRadius, box.StrokeWidth, box.StrokeColor = 8, 1, theme.Color(theme.ColorNameInputBorder)
			field = container.NewStack(box, field)
		}
		parts = append(parts, field)
		if it.HintText != "" {
			parts = append(parts, lowLabel(it.HintText))
		}
		fields = append(fields, vbox(4, parts...))
	}
	body := container.New(layout.NewCustomPaddedLayout(2, 14, 24, 24), vbox(14, fields...))

	heading := canvas.NewText(title, uitheme.CurrentForeground())
	heading.TextSize = 18
	heading.TextStyle = fyne.TextStyle{Bold: true}
	head := container.New(layout.NewCustomPaddedLayout(22, 10, 24, 24), heading)

	var overlay fyne.CanvasObject
	canvasOf := win.Canvas()
	prevKey := canvasOf.OnTypedKey()
	closeSheet := func() {
		canvasOf.SetOnTypedKey(prevKey)
		canvasOf.Overlays().Remove(overlay)
	}
	cancel := widget.NewButton("Cancel", closeSheet)
	save := widget.NewButton(confirm, func() {
		closeSheet()
		done()
	})
	save.Importance = widget.HighImportance
	foot := container.New(layout.NewCustomPaddedLayout(12, 14, 24, 24), container.NewHBox(layout.NewSpacer(), cancel, save))

	surface := canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	surface.CornerRadius = 14
	surface.StrokeWidth = 1
	surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
	sheet := container.NewStack(surface, container.NewBorder(head, container.NewVBox(widget.NewSeparator(), foot), nil, nil, container.NewVScroll(body)))

	if size.Width == 0 {
		size.Width = 520
	}
	if size.Height == 0 {
		// Tall enough for every field; the layout shrinks it to fit the window, and the body scrolls.
		size.Height = body.MinSize().Height + head.MinSize().Height + foot.MinSize().Height + 24
	}
	// A scrim and a card on it, not a PopUp: a PopUp draws a square backdrop
	// that shows behind the card's rounded corners.
	scrim := canvas.NewRectangle(color.NRGBA{A: 0x66})
	overlay = container.New(&sheetLayout{size}, scrim, sheet)
	canvasOf.SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			closeSheet()
			return
		}
		if prevKey != nil {
			prevKey(e)
		}
	})
	overlay.Resize(canvasOf.Size()) // the canvas sizes a PopUp for you, but not a plain overlay
	canvasOf.Overlays().Add(overlay)
}

// sheetLayout stretches a scrim over the window and centres a card on it,
// kept 20px from the sides and 24px from the top and bottom.
type sheetLayout struct{ size fyne.Size }

func (l *sheetLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Resize(size)
	objs[0].Move(fyne.NewPos(0, 0))
	card := fyne.NewSize(min(l.size.Width, size.Width-40), min(l.size.Height, size.Height-48))
	objs[1].Resize(card)
	objs[1].Move(fyne.NewPos((size.Width-card.Width)/2, (size.Height-card.Height)/2))
}

func (l *sheetLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

// ---------- providers ----------

func (a *app) providersTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	providers := append([]provider.Provider(nil), a.cfg.Providers...)
	active := map[string]string{}
	for k, v := range a.cfg.ActiveProvider {
		active[k] = v
	}
	a.mu.Unlock()

	var blocks []fyne.CanvasObject
	if found := envcheck.Scan(); len(found) > 0 {
		var rows []fyne.CanvasObject
		for _, f := range found {
			f := f
			where := "Set in the running environment"
			if f.File != "" {
				where = f.File
				if f.Line > 0 {
					where = fmt.Sprintf("%s, line %d", f.File, f.Line)
				}
			}
			var fix []fyne.CanvasObject
			if f.File != "" && f.Line > 0 {
				fix = append(fix, homeLink(c, "Comment out", func() { a.runOp(func() error { return a.doDisableEnv(f) }) }))
			}
			rows = append(rows, prefItem(c, f.Name, "", clip(where, 64), fix...))
		}
		blocks = append(blocks, prefSection(c, "Environment conflicts"), prefGroup(c, rows...),
			flushNote("These variables override what is set here in every terminal that loads them."))
	}

	for _, tool := range []string{harness.Claude, harness.Codex} {
		tool := tool
		rows := []fyne.CanvasObject{a.providerRow(c, tool, provider.Provider{Name: "Official", Harness: tool}, active[tool] == "")}
		for _, p := range providers {
			if p.Harness == tool {
				rows = append(rows, a.providerRow(c, tool, p, active[tool] == p.ID))
			}
		}
		blocks = append(blocks,
			prefSectionWith(c, harness.Title(tool), homeLink(c, "Add provider", func() { a.providerForm(tool, provider.Provider{Harness: tool}) })),
			prefGroup(c, rows...))
	}

	state := cctoggles.State()
	hasClaude := harness.Installed(harness.Claude, "")
	var toggles []fyne.CanvasObject
	for _, t := range cctoggles.All {
		id := t.ID
		toggles = append(toggles, prefRow(c, t.Title, t.Desc, prefToggle(c, state[id], hasClaude, func(on bool) {
			a.runOp(func() error { return a.doSetToggle(id, on) })
		})))
	}
	blocks = append(blocks, prefSection(c, "Claude Code settings"), prefGroup(c, toggles...))
	return prefPage(pageHeader(c, "Providers", "Point Claude Code or Codex at another model API. Only the endpoint, key and model settings change; everything else in their config stays as it is."), blocks...)
}

// providerRow is one provider: its endpoint and model, then Use (or an
// "In use" tag by the name) and, for the ones you added, Test, Edit and Remove.
func (a *app) providerRow(c homeColors, tool string, p provider.Provider, inUse bool) fyne.CanvasObject {
	detail := p.BaseURL
	if p.ID == "" {
		detail = "Your own sign-in"
	}
	if p.Model != "" {
		detail += " · " + p.Model
	}
	tag := ""
	var right []fyne.CanvasObject
	if inUse {
		tag = "In use"
	} else {
		id := p.ID
		right = append(right, homeBtn(c, "Use", true, func() { a.runOp(func() error { return a.doProviderUse(tool, id) }) }))
	}
	if p.ID != "" {
		right = append(right,
			homeLink(c, "Test", func() { a.probeProvider(p) }),
			homeLink(c, "Edit", func() { a.providerForm(tool, p) }),
			homeRedLink(c, "Remove", func() { a.runOp(func() error { return a.doProviderDelete(p.ID) }) }),
		)
	}
	return prefItem(c, clip(p.Name, 32), tag, clip(detail, 56), right...)
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
	c := homePalette()
	a.mu.Lock()
	sources := map[string]profile.SkillSource{}
	for k, v := range a.cfg.SkillSources {
		sources[k] = v
	}
	a.mu.Unlock()

	header := prefHeader(c, "Skills", "Installed once in ~/.agents/skills, which Codex, Gemini CLI, Cursor, OpenCode and Qwen Code read, and linked into ~/.claude/skills for Claude Code.",
		homeBtn(c, "From folder…", false, func() { a.runOp(a.doSkillInstallFolder) }),
		homeBtn(c, "From zip…", false, func() { a.runOp(a.doSkillInstallZip) }),
		homeBtn(c, "From GitHub…", true, func() {
			src := widget.NewEntry()
			src.SetPlaceHolder("anthropics/skills/skills/pdf")
			a.showForm("Install skills from GitHub", "Install", []*widget.FormItem{
				widget.NewFormItem("Repository path", src),
			}, fyne.NewSize(520, 0), func() {
				s := src.Text
				a.runOp(func() error { return a.doSkillInstall(s) })
			})
		}))

	list := skills.List()
	if len(list) == 0 {
		return prefPage(header, prefEmpty(c, "No skills installed yet. Add one from a folder, a zip file or a GitHub repository."))
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
			right = append(right, homeBtn(c, "Share with every tool", false, func() { a.runOp(func() error { return a.doSkillShare(s.Name) }) }))
		}
		if sources[s.Name].Source != "" {
			right = append(right, homeLink(c, "Update", func() { a.runOp(func() error { return a.doSkillUpdate(s.Name) }) }))
		}
		right = append(right, homeRedLink(c, "Remove", func() { a.runOp(func() error { return a.doSkillRemove(s.Name) }) }))
		rows = append(rows, prefItem(c, clip(s.Name, 40), "", clip(where, 56), right...))
	}
	return prefPage(header, prefSection(c, fmt.Sprintf("Installed · %d", len(list))), prefGroup(c, rows...))
}

// ---------- instructions ----------

func (a *app) instructionsTabContent() fyne.CanvasObject {
	c := homePalette()
	a.mu.Lock()
	entries := append([]instructions.Entry(nil), a.cfg.Instructions...)
	activeID := a.cfg.ActiveInstructions
	tools := map[string]bool{}
	for k, v := range a.cfg.InstructionTools {
		tools[k] = v
	}
	a.mu.Unlock()

	header := prefHeader(c, "Instructions", "One set of global instructions written to each tool's own file. Project files are left alone.",
		homeBtn(c, "New instructions", true, func() { a.instructionsForm(instructions.Entry{}) }))

	var rows []fyne.CanvasObject
	var activeBody string
	for _, e := range entries {
		e := e
		preview := strings.TrimSpace(e.Body)
		if i := strings.IndexByte(preview, '\n'); i >= 0 {
			preview = preview[:i]
		}
		tag := ""
		var right []fyne.CanvasObject
		if e.ID == activeID {
			activeBody = e.Body
			tag = "In use"
		} else {
			right = append(right, homeBtn(c, "Use", true, func() { a.runOp(func() error { return a.doInstrApply(e.ID) }) }))
		}
		right = append(right,
			homeLink(c, "Edit", func() { a.instructionsForm(e) }),
			homeRedLink(c, "Remove", func() { a.runOp(func() error { return a.doInstrDelete(e.ID) }) }),
		)
		rows = append(rows, prefItem(c, clip(e.Name, 40), tag, clip(preview, 64), right...))
	}
	saved := prefEmpty(c, "No instructions saved yet. New instructions starts a set.")
	if len(rows) > 0 {
		saved = prefGroup(c, rows...)
	}
	blocks := []fyne.CanvasObject{prefSection(c, "Saved"), saved}

	sync := instructions.InSync(activeBody)
	var toolRows []fyne.CanvasObject
	for _, t := range instructions.Targets() {
		t := t
		desc := t.Path
		if activeID != "" && tools[t.Tool] && !sync[t.Tool] {
			desc += " · changed outside CD-Switcher"
		}
		// prefItem, not prefRow: a path has no spaces to wrap at.
		toolRows = append(toolRows, prefItem(c, harness.Title(t.Tool), "", clip(desc, 76), prefToggle(c, tools[t.Tool], true, func(on bool) {
			a.runOp(func() error { return a.doInstrSetTool(t.Tool, on) })
		})))
	}
	if len(toolRows) > 0 {
		blocks = append(blocks, prefSection(c, "Write to"), prefGroup(c, toolRows...))
	}
	return prefPage(header, blocks...)
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

// ---------- Manage Profiles additions ----------

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
			items = append(items, gaugeItem(fmt.Sprintf("Session %.0f%% · resets %s", e.quota.Session.Percent, e.quota.Session.ResetsAt.Local().Format("3:04pm")), e.quota.Session.Percent, a.onTrayTap))
		}
		if e.quota.Weekly.Seconds > 0 {
			items = append(items, gaugeItem(fmt.Sprintf("Weekly %.0f%% · resets %s", e.quota.Weekly.Percent, e.quota.Weekly.ResetsAt.Local().Format("Mon Jan 2")), e.quota.Weekly.Percent, a.onTrayTap))
		}
	}
	if !cp.fetchedAt.IsZero() && !cp.quota.Unlimited {
		if len(items) > 0 {
			items = append(items, fyne.NewMenuItemSeparator())
		}
		items = append(items, gaugeItem(fmt.Sprintf("Copilot premium %.0f%% · %d left", cp.quota.Percent, cp.quota.Remaining), cp.quota.Percent, a.onTrayTap))
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
