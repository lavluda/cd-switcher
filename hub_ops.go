package main

// Operations for everything beyond Claude Desktop accounts: Codex accounts,
// other tools' quotas, providers, MCP servers, skills, instructions, the
// terminal launcher, export/import and cdswitcher:// links. Like the account
// operations in main.go, the do* functions run on the op worker with a.mu
// held; dialogs inside them go through a.unlocked.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/zenity"

	"github.com/lavluda/cd-switcher/internal/cctoggles"
	"github.com/lavluda/cd-switcher/internal/claude"
	"github.com/lavluda/cd-switcher/internal/codex"
	"github.com/lavluda/cd-switcher/internal/copilot"
	"github.com/lavluda/cd-switcher/internal/deeplink"
	"github.com/lavluda/cd-switcher/internal/envcheck"
	"github.com/lavluda/cd-switcher/internal/harness"
	"github.com/lavluda/cd-switcher/internal/instructions"
	"github.com/lavluda/cd-switcher/internal/mcp"
	"github.com/lavluda/cd-switcher/internal/profile"
	"github.com/lavluda/cd-switcher/internal/provider"
	"github.com/lavluda/cd-switcher/internal/ratelimits"
	"github.com/lavluda/cd-switcher/internal/skills"
	"github.com/lavluda/cd-switcher/internal/terminal"
	"github.com/lavluda/cd-switcher/internal/usage"
)

// codexEntry and copilotEntry are the last-known quotas, guarded by statsMu.
// A failed fetch keeps the previous quota and only records the error.
type codexEntry struct {
	quota     codex.Quota
	fetchedAt time.Time
	err       error
}

type copilotEntry struct {
	quota     copilot.Quota
	fetchedAt time.Time
	err       error
}

// queueOp hands an operation to the worker and waits for room in the queue,
// for background work (a finished sign-in, a confirmed link) that must not be
// dropped the way an extra click is.
func (a *app) queueOp(fn func() error) { a.ops <- fn }

func (a *app) backupDir() string { return filepath.Join(a.store.Root(), "backups") }

func (a *app) desktopDir() string {
	dir, err := claude.DataDir()
	if err != nil {
		return ""
	}
	return dir
}

// confirm asks a yes/no question with the state lock released.
func (a *app) confirm(title, msg, ok string) error {
	return a.unlocked(func() error {
		return zenity.Question(msg, zenity.Title(title), zenity.OKLabel(ok))
	})
}

func joinErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// ---------- Codex accounts ----------

func (a *app) codexSavedPath(id string) string {
	return filepath.Join(a.store.Root(), "codex-accounts", id, "auth.json")
}

func sameCodex(id codex.Identity, acct profile.CodexAccount) bool {
	return id.AccountID != "" && id.AccountID == acct.AccountID && strings.EqualFold(id.Email, acct.Email)
}

// codexImport saves the login at src under the account it belongs to,
// adding the account when it is new, and returns the account's ID. ChatGPT
// team members share an account ID, so the email is part of the match.
func (a *app) codexImport(src string, ident codex.Identity) (string, error) {
	for i, x := range a.cfg.CodexAccounts {
		if sameCodex(ident, x) {
			if err := codex.Copy(src, a.codexSavedPath(x.ID)); err != nil {
				return "", err
			}
			a.cfg.CodexAccounts[i].Plan = ident.Plan
			return x.ID, nil
		}
	}
	label := ident.Email
	if label == "" {
		label = "Codex"
	}
	id := profile.NewHubID("codex-"+label, func(s string) bool { _, ok := a.cfg.FindCodex(s); return ok })
	if err := codex.Copy(src, a.codexSavedPath(id)); err != nil {
		return "", err
	}
	a.cfg.CodexAccounts = append(a.cfg.CodexAccounts, profile.CodexAccount{
		ID: id, Label: label, Email: ident.Email, Plan: ident.Plan, AccountID: ident.AccountID, AddedAt: time.Now(),
	})
	return id, nil
}

// doCodexAddCurrent saves the login Codex is using now and marks it active.
func (a *app) doCodexAddCurrent() error {
	live, cleanup, err := codex.LiveFile()
	if err != nil {
		return err
	}
	defer cleanup()
	ident, err := codex.ReadIdentity(live)
	if err != nil {
		return fmt.Errorf("Codex is not signed in on this Mac (%v). Run codex login first, or use Sign in to another account", err)
	}
	id, err := a.codexImport(live, ident)
	if err != nil {
		return err
	}
	a.cfg.ActiveCodex = id
	return a.store.Save(a.cfg)
}

// startCodexLogin runs `codex login` in a throwaway CODEX_HOME, outside the
// op queue since the browser sign-in can take minutes, then imports the
// result. The live Codex login is not touched.
func (a *app) startCodexLogin() {
	if !a.codexLoginRunning.CompareAndSwap(false, true) {
		_ = zenity.Notify("A Codex sign-in is already waiting in your browser.")
		return
	}
	go func() {
		defer a.codexLoginRunning.Store(false)
		_ = zenity.Notify("Sign in to ChatGPT in the browser window Codex opens.")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		path, cleanup, err := codex.Login(ctx)
		cancel()
		if err != nil {
			notifyErr("Codex sign-in failed", err)
			return
		}
		a.queueOp(func() error {
			defer cleanup()
			ident, err := codex.ReadIdentity(path)
			if err != nil {
				return err
			}
			id, err := a.codexImport(path, ident)
			if err != nil {
				return err
			}
			if a.cfg.ActiveCodex == "" {
				// Nothing was active: this becomes the live login.
				if err := codex.UseLogin(a.codexSavedPath(id)); err != nil {
					return err
				}
				a.cfg.ActiveCodex = id
			}
			acct, _ := a.cfg.FindCodex(id)
			_ = zenity.Notify("Added Codex account " + acct.Label)
			go a.refreshStats(0)
			return a.store.Save(a.cfg)
		})
	}()
}

// doCodexSwitch makes id the live Codex login. The outgoing account's file is
// saved first, since Codex rewrites auth.json in place when it refreshes.
func (a *app) doCodexSwitch(id string) error {
	target, ok := a.cfg.FindCodex(id)
	if !ok {
		return fmt.Errorf("Codex account %q not found", id)
	}
	if id == a.cfg.ActiveCodex {
		return nil
	}
	live, cleanup, err := codex.LiveFile()
	if err != nil {
		return err
	}
	defer cleanup()
	if ident, err := codex.ReadIdentity(live); err == nil {
		// Whoever is signed in now is saved before the login is replaced:
		// the active account, or a login made by hand with codex login.
		if _, err := a.codexImport(live, ident); err != nil {
			return fmt.Errorf("save the current Codex login first: %w", err)
		}
	}
	if err := codex.UseLogin(a.codexSavedPath(id)); err != nil {
		return err
	}
	a.cfg.ActiveCodex = id
	logf("codex: switched to %q", target.Label)
	_ = zenity.Notify("Codex now uses " + target.Label + ". Restart any running codex sessions.")
	return a.store.Save(a.cfg)
}

func (a *app) doCodexRename(id string) error {
	acct, ok := a.cfg.FindCodex(id)
	if !ok {
		return fmt.Errorf("Codex account %q not found", id)
	}
	label, err := a.askLabel("New name for this Codex account:", "Rename Codex account", acct.Label)
	if err != nil || label == "" || label == acct.Label {
		return err
	}
	for i := range a.cfg.CodexAccounts {
		if a.cfg.CodexAccounts[i].ID == id {
			a.cfg.CodexAccounts[i].Label = label
		}
	}
	return a.store.Save(a.cfg)
}

func (a *app) doCodexRemove(id string) error {
	acct, ok := a.cfg.FindCodex(id)
	if !ok {
		return nil
	}
	msg := fmt.Sprintf("Remove the saved Codex login “%s”?\n\nCodex itself stays signed in as it is now.", acct.Label)
	if err := a.confirm("Remove Codex account", msg, "Remove"); err != nil {
		return err
	}
	var keep []profile.CodexAccount
	for _, x := range a.cfg.CodexAccounts {
		if x.ID != id {
			keep = append(keep, x)
		}
	}
	a.cfg.CodexAccounts = keep
	if a.cfg.ActiveCodex == id {
		a.cfg.ActiveCodex = ""
	}
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	a.statsMu.Lock()
	delete(a.codexCache, id)
	a.statsMu.Unlock()
	return os.RemoveAll(filepath.Dir(a.codexSavedPath(id)))
}

// ---------- quotas beyond Claude ----------

// refreshOtherQuotas fetches Codex and Copilot quotas older than minAge.
func (a *app) refreshOtherQuotas(minAge time.Duration) {
	a.mu.Lock()
	accts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	active := a.cfg.ActiveCodex
	a.mu.Unlock()

	for _, acct := range accts {
		a.statsMu.Lock()
		prev := a.codexCache[acct.ID]
		a.statsMu.Unlock()
		if time.Since(prev.fetchedAt) < minAge && prev.err == nil {
			continue
		}
		path, canRefresh, cleanup := a.codexSavedPath(acct.ID), true, func() {}
		if acct.ID == active {
			// Codex keeps the live login fresh itself; refreshing it here
			// would spend a refresh token Codex still holds.
			live, done, err := codex.LiveFile()
			if err != nil {
				logf("codex usage for %q: %v", acct.Label, err)
				continue
			}
			path, canRefresh, cleanup = live, false, done
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		q, err := codex.Usage(ctx, path, canRefresh)
		cancel()
		cleanup()
		next := codexEntry{quota: q, fetchedAt: time.Now(), err: err}
		if err != nil {
			logf("codex usage for %q: %v", acct.Label, err)
			next.quota, next.fetchedAt = prev.quota, prev.fetchedAt
		}
		a.statsMu.Lock()
		if a.codexCache == nil {
			a.codexCache = map[string]codexEntry{}
		}
		a.codexCache[acct.ID] = next
		a.statsMu.Unlock()
		if err == nil && acct.ID == active {
			a.maybeAutoSwitchCodex(acct.ID, q)
		}
	}

	a.statsMu.Lock()
	prev := a.copilot
	a.statsMu.Unlock()
	if time.Since(prev.fetchedAt) >= minAge || prev.err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		q, err := copilot.Fetch(ctx)
		cancel()
		next := copilotEntry{quota: q, fetchedAt: time.Now(), err: err}
		if err != nil {
			if !errors.Is(err, copilot.ErrNoLogin) {
				logf("copilot quota: %v", err)
			}
			next.quota, next.fetchedAt = prev.quota, prev.fetchedAt
		}
		a.statsMu.Lock()
		a.copilot = next
		a.statsMu.Unlock()
	}
}

// maybeAutoSwitchCodex moves Codex to the saved account with the most room
// once the active one reaches the auto-switch percent, once per window.
func (a *app) maybeAutoSwitchCodex(id string, q codex.Quota) {
	a.mu.Lock()
	enabled := a.cfg.AutoSwitchOnLimit
	threshold := float64(a.cfg.AutoSwitchPercent())
	accts := append([]profile.CodexAccount(nil), a.cfg.CodexAccounts...)
	a.mu.Unlock()
	used := max(q.Session.Percent, q.Weekly.Percent)
	if !enabled || len(accts) < 2 {
		return
	}
	if used < threshold {
		a.forgetAlerts(id, "codex-autoswitch")
		return
	}
	var loads []usage.AccountLoad
	a.statsMu.Lock()
	for _, x := range accts {
		e, ok := a.codexCache[x.ID]
		loads = append(loads, usage.AccountLoad{
			ID: x.ID, Session: e.quota.Session.Percent, Week: e.quota.Weekly.Percent,
			ResetsIn: time.Until(e.quota.Session.ResetsAt),
			Ready:    ok && !e.fetchedAt.IsZero() && e.err == nil && x.ID != id,
		})
	}
	a.statsMu.Unlock()
	best, ok := usage.Freest(loads)
	if !ok || max(best.Session, best.Week) >= threshold {
		return
	}
	reset := q.Session.ResetsAt
	if q.Weekly.Percent >= threshold {
		reset = q.Weekly.ResetsAt
	}
	key := alertKey{id: id, window: "codex-autoswitch", resetUnix: reset.Unix()}
	a.alertMu.Lock()
	if a.alerted == nil {
		a.alerted = map[alertKey]struct{}{}
	}
	_, seen := a.alerted[key]
	a.alerted[key] = struct{}{}
	a.alertMu.Unlock()
	if seen {
		return
	}
	logf("codex auto-switch: %q at %.0f%%, moving to %q", id, used, best.ID)
	a.runOp(func() error { return a.doCodexSwitch(best.ID) })
}

// ---------- Claude Code statusline limits ----------

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

func (a *app) doSetStatusline(on bool) error {
	if !on {
		return ratelimits.Uninstall(a.store.Root())
	}
	exe, err := selfPath()
	if err != nil {
		return err
	}
	return ratelimits.Install(a.store.Root(), exe)
}

// ---------- providers ----------

func (a *app) providerKeys() provider.Keys {
	return provider.Keys{Path: filepath.Join(a.store.Root(), "provider-keys.json")}
}

// doProviderSave adds a provider, or replaces the one with the same ID. An
// empty key keeps the stored one. A provider in use is applied again.
func (a *app) doProviderSave(p provider.Provider, key string) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("a provider needs a name")
	}
	if p.Harness == harness.Codex && p.BaseURL == "" {
		return errors.New("a Codex provider needs a base URL")
	}
	if p.ID == "" {
		p.ID = profile.NewHubID(p.Harness+"-"+p.Name, func(s string) bool { _, ok := a.cfg.FindProvider(s); return ok })
		a.cfg.Providers = append(a.cfg.Providers, p)
	} else {
		for i := range a.cfg.Providers {
			if a.cfg.Providers[i].ID == p.ID {
				a.cfg.Providers[i] = p
			}
		}
	}
	if key != "" {
		if err := a.providerKeys().Set(p.ID, key); err != nil {
			return err
		}
	}
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	if a.cfg.ActiveProvider[p.Harness] == p.ID {
		return a.doProviderUse(p.Harness, p.ID)
	}
	return nil
}

// doProviderUse points a tool at a provider, or at the official setup for "".
func (a *app) doProviderUse(tool, id string) error {
	var p *provider.Provider
	key := ""
	if id != "" {
		found, ok := a.cfg.FindProvider(id)
		if !ok {
			return fmt.Errorf("provider %q not found", id)
		}
		p, key = &found, a.providerKeys().Get(id)
	}
	var err error
	switch tool {
	case harness.Claude:
		err = provider.ApplyClaude(p, key, &a.cfg.ProviderSaved)
	case harness.Codex:
		err = provider.ApplyCodex(p, key, &a.cfg.ProviderSaved)
	default:
		return fmt.Errorf("unknown tool %q", tool)
	}
	if err != nil {
		return err
	}
	if a.cfg.ActiveProvider == nil {
		a.cfg.ActiveProvider = map[string]string{}
	}
	if id == "" {
		delete(a.cfg.ActiveProvider, tool)
	} else {
		a.cfg.ActiveProvider[tool] = id
	}
	name := "the official setup"
	if p != nil {
		name = p.Name
	}
	note := "New Claude Code requests use it right away."
	if tool == harness.Codex {
		note = "Restart running codex sessions to pick it up."
	}
	_ = zenity.Notify(fmt.Sprintf("%s now uses %s. %s", harness.Title(tool), name, note))
	return a.store.Save(a.cfg)
}

func (a *app) doProviderDelete(id string) error {
	p, ok := a.cfg.FindProvider(id)
	if !ok {
		return nil
	}
	if err := a.confirm("Remove provider", fmt.Sprintf("Remove the provider “%s” and its stored key?", p.Name), "Remove"); err != nil {
		return err
	}
	if a.cfg.ActiveProvider[p.Harness] == id {
		if err := a.doProviderUse(p.Harness, ""); err != nil {
			return err
		}
	}
	var keep []provider.Provider
	for _, x := range a.cfg.Providers {
		if x.ID != id {
			keep = append(keep, x)
		}
	}
	a.cfg.Providers = keep
	if err := a.providerKeys().Set(id, ""); err != nil {
		return err
	}
	return a.store.Save(a.cfg)
}

// probeProvider measures a provider's endpoint and reports it in a
// notification. It runs off the op queue: it touches no state.
func (a *app) probeProvider(p provider.Provider) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		d, err := provider.Probe(ctx, p.BaseURL)
		if err != nil {
			_ = zenity.Notify(fmt.Sprintf("%s: %v", p.Name, err))
			return
		}
		_ = zenity.Notify(fmt.Sprintf("%s answered in %d ms", p.Name, d.Milliseconds()))
	}()
}

// ---------- environment conflicts and Claude Code toggles ----------

func (a *app) doDisableEnv(f envcheck.Finding) error {
	msg := fmt.Sprintf("Comment out this line?\n\n%s\n\nA copy of the file is saved first. Open a new terminal afterwards.", f)
	if err := a.confirm("Environment variable", msg, "Comment out"); err != nil {
		return err
	}
	return envcheck.Disable(f, a.backupDir())
}

func (a *app) doSetToggle(id string, on bool) error { return cctoggles.Set(id, on) }

// ---------- MCP ----------

// doMCPImport merges the servers the tools already have into the list.
func (a *app) doMCPImport() error {
	found, errs := mcp.Import(a.desktopDir())
	for _, s := range found {
		merged := false
		for i := range a.cfg.MCPServers {
			if a.cfg.MCPServers[i].Name == s.Name {
				if a.cfg.MCPServers[i].Enabled == nil {
					a.cfg.MCPServers[i].Enabled = map[string]bool{}
				}
				for tool := range s.Enabled {
					a.cfg.MCPServers[i].Enabled[tool] = true
				}
				merged = true
			}
		}
		if !merged {
			a.cfg.MCPServers = append(a.cfg.MCPServers, s)
		}
	}
	a.cfg.MCPImported = true
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	return joinErrs(errs)
}

func (a *app) findMCP(name string) (int, bool) {
	for i, s := range a.cfg.MCPServers {
		if s.Name == name {
			return i, true
		}
	}
	return -1, false
}

// doMCPToggle turns one server on or off for one tool.
func (a *app) doMCPToggle(name, tool string, on bool) error {
	i, ok := a.findMCP(name)
	if !ok {
		return fmt.Errorf("MCP server %q not found", name)
	}
	s := &a.cfg.MCPServers[i]
	if s.Enabled == nil {
		s.Enabled = map[string]bool{}
	}
	s.Enabled[tool] = on
	if err := joinErrs(mcp.Apply(a.desktopDir(), []mcp.Server{*s}, nil, nil)); err != nil {
		return err
	}
	return a.store.Save(a.cfg)
}

// doMCPSave adds a server, or replaces the one named oldName, and writes it
// to every tool it is enabled for.
func (a *app) doMCPSave(s mcp.Server, oldName string) error {
	if err := s.Valid(); err != nil {
		return err
	}
	removed := map[string]bool{}
	if i, ok := a.findMCP(oldName); ok && oldName != "" {
		if s.Enabled == nil {
			s.Enabled = a.cfg.MCPServers[i].Enabled
		}
		a.cfg.MCPServers = append(a.cfg.MCPServers[:i], a.cfg.MCPServers[i+1:]...)
		if oldName != s.Name {
			removed[oldName] = true
		}
	}
	if _, clash := a.findMCP(s.Name); clash {
		return fmt.Errorf("an MCP server named %q already exists", s.Name)
	}
	if s.Enabled == nil {
		s.Enabled = map[string]bool{}
		for _, tool := range mcp.Tools {
			s.Enabled[tool] = harness.Installed(tool, a.desktopDir()) && mcp.Supports(tool, s)
		}
	}
	a.cfg.MCPServers = append(a.cfg.MCPServers, s)
	errs := mcp.Apply(a.desktopDir(), []mcp.Server{s}, map[string]bool{s.Name: true}, removed)
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	return joinErrs(errs)
}

func (a *app) doMCPDelete(name string) error {
	i, ok := a.findMCP(name)
	if !ok {
		return nil
	}
	if err := a.confirm("Remove MCP server", fmt.Sprintf("Remove “%s” from every tool?", name), "Remove"); err != nil {
		return err
	}
	a.cfg.MCPServers = append(a.cfg.MCPServers[:i], a.cfg.MCPServers[i+1:]...)
	errs := mcp.Apply(a.desktopDir(), nil, nil, map[string]bool{name: true})
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	return joinErrs(errs)
}

// ---------- skills ----------

func (a *app) recordSkills(names []string, source string) {
	if a.cfg.SkillSources == nil {
		a.cfg.SkillSources = map[string]profile.SkillSource{}
	}
	for _, n := range names {
		h, _ := skills.Hash(filepath.Join(harness.AgentsSkills(), n))
		a.cfg.SkillSources[n] = profile.SkillSource{Source: source, Hash: h}
	}
}

// doSkillInstall installs every skill at a GitHub location. The download
// runs with the state lock released.
func (a *app) doSkillInstall(src string) error {
	s, err := skills.ParseSource(src)
	if err != nil {
		return err
	}
	var names []string
	err = a.unlocked(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		names, err = skills.InstallGitHub(ctx, s)
		return err
	})
	if err != nil {
		return err
	}
	a.recordSkills(names, s.String())
	_ = zenity.Notify("Installed skills: " + strings.Join(names, ", "))
	return a.store.Save(a.cfg)
}

func (a *app) doSkillInstallZip() error {
	var names []string
	err := a.unlocked(func() error {
		path, err := zenity.SelectFile(zenity.Title("Install skill from zip"), zenity.FileFilters{{Name: "Zip archives", Patterns: []string{"*.zip"}}})
		if err != nil {
			return err
		}
		names, err = skills.InstallZip(path)
		return err
	})
	if err != nil {
		return err
	}
	a.recordSkills(names, "")
	_ = zenity.Notify("Installed skills: " + strings.Join(names, ", "))
	return a.store.Save(a.cfg)
}

// doSkillInstallFolder copies a local skill folder (or a folder of skills)
// into the shared folder.
func (a *app) doSkillInstallFolder() error {
	var names []string
	err := a.unlocked(func() error {
		path, err := zenity.SelectFile(zenity.Title("Install skills from a folder"), zenity.Directory())
		if err != nil {
			return err
		}
		names, err = skills.InstallDir(path)
		return err
	})
	if err != nil {
		return err
	}
	a.recordSkills(names, "")
	_ = zenity.Notify("Installed skills: " + strings.Join(names, ", "))
	return a.store.Save(a.cfg)
}

// doSkillUpdate installs a skill's source again and says whether it changed.
func (a *app) doSkillUpdate(name string) error {
	src, ok := a.cfg.SkillSources[name]
	if !ok || src.Source == "" {
		return fmt.Errorf("%s was not installed from GitHub, so there is nothing to update from", name)
	}
	before := src.Hash
	if err := a.doSkillInstall(src.Source); err != nil {
		return err
	}
	if a.cfg.SkillSources[name].Hash == before {
		_ = zenity.Notify(name + " is already up to date.")
	}
	return nil
}

func (a *app) doSkillShare(name string) error { return skills.Share(name) }

func (a *app) doSkillRemove(name string) error {
	if err := a.confirm("Remove skill", fmt.Sprintf("Remove the skill “%s”? A copy is kept in CD-Switcher's backups.", name), "Remove"); err != nil {
		return err
	}
	if err := skills.Remove(name, filepath.Join(a.backupDir(), "skills")); err != nil {
		return err
	}
	delete(a.cfg.SkillSources, name)
	return a.store.Save(a.cfg)
}

// ---------- instructions ----------

// loadInstructionsOnce reads the tools' current instruction files into the
// list the first time, without writing anything.
func (a *app) loadInstructionsOnce() error {
	if a.cfg.InstructionsLoaded {
		return nil
	}
	a.cfg.Instructions = append(a.cfg.Instructions, instructions.Existing()...)
	if a.cfg.InstructionTools == nil {
		a.cfg.InstructionTools = map[string]bool{}
		for _, t := range instructions.Targets() {
			a.cfg.InstructionTools[t.Tool] = true
		}
	}
	a.cfg.InstructionsLoaded = true
	return a.store.Save(a.cfg)
}

func (a *app) findInstr(id string) (int, bool) {
	for i, e := range a.cfg.Instructions {
		if e.ID == id {
			return i, true
		}
	}
	return -1, false
}

func (a *app) doInstrSave(e instructions.Entry) error {
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("give the instructions a name")
	}
	if i, ok := a.findInstr(e.ID); ok && e.ID != "" {
		a.cfg.Instructions[i] = e
	} else {
		e.ID = profile.NewHubID(e.Name, func(s string) bool { _, ok := a.findInstr(s); return ok })
		a.cfg.Instructions = append(a.cfg.Instructions, e)
	}
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	if a.cfg.ActiveInstructions == e.ID {
		return a.doInstrApply(e.ID)
	}
	return nil
}

// doInstrApply writes an instruction set to the chosen tools' files.
func (a *app) doInstrApply(id string) error {
	i, ok := a.findInstr(id)
	if !ok {
		return fmt.Errorf("instructions %q not found", id)
	}
	if err := joinErrs(instructions.Apply(a.cfg.Instructions[i].Body, a.cfg.InstructionTools)); err != nil {
		return err
	}
	a.cfg.ActiveInstructions = id
	return a.store.Save(a.cfg)
}

func (a *app) doInstrSetTool(tool string, on bool) error {
	if a.cfg.InstructionTools == nil {
		a.cfg.InstructionTools = map[string]bool{}
	}
	a.cfg.InstructionTools[tool] = on
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	if on && a.cfg.ActiveInstructions != "" {
		return a.doInstrApply(a.cfg.ActiveInstructions)
	}
	return nil
}

func (a *app) doInstrDelete(id string) error {
	i, ok := a.findInstr(id)
	if !ok {
		return nil
	}
	if err := a.confirm("Remove instructions", fmt.Sprintf("Remove “%s”? The tools' files keep their current text.", a.cfg.Instructions[i].Name), "Remove"); err != nil {
		return err
	}
	a.cfg.Instructions = append(a.cfg.Instructions[:i], a.cfg.Instructions[i+1:]...)
	if a.cfg.ActiveInstructions == id {
		a.cfg.ActiveInstructions = ""
	}
	return a.store.Save(a.cfg)
}

// ---------- terminal ----------

func (a *app) doSetTerminal(app string) error {
	a.cfg.Terminal = app
	return a.store.Save(a.cfg)
}

// openInTerminal starts a tool's CLI in the chosen terminal app.
func (a *app) openInTerminal(command string) {
	a.mu.Lock()
	app := a.cfg.Terminal
	a.mu.Unlock()
	go func() {
		if err := terminal.Open(app, command); err != nil {
			notifyErr("Could not open the terminal", err)
		}
	}()
}

// ---------- export / import ----------

// exportFile is the shareable part of the setup: no accounts, no keys.
type exportFile struct {
	Format           string               `json:"format"`
	Providers        []provider.Provider  `json:"providers,omitempty"`
	MCPServers       []mcp.Server         `json:"mcpServers,omitempty"`
	Skills           []string             `json:"skills,omitempty"`
	Instructions     []instructions.Entry `json:"instructions,omitempty"`
	InstructionTools map[string]bool      `json:"instructionTools,omitempty"`
	Terminal         string               `json:"terminal,omitempty"`
}

const exportFormat = "cd-switcher-export-1"

func (a *app) doExport() error {
	var path string
	err := a.unlocked(func() error {
		var err error
		path, err = zenity.SelectFileSave(zenity.Title("Export CD-Switcher setup"), zenity.Filename("cd-switcher-setup.json"), zenity.ConfirmOverwrite())
		return err
	})
	if err != nil {
		return err
	}
	out := exportFile{Format: exportFormat, Providers: a.cfg.Providers, MCPServers: a.cfg.MCPServers,
		Instructions: a.cfg.Instructions, InstructionTools: a.cfg.InstructionTools, Terminal: a.cfg.Terminal}
	for _, s := range a.cfg.SkillSources {
		if s.Source != "" {
			out.Skills = append(out.Skills, s.Source)
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	_ = zenity.Notify("Exported providers, MCP servers, skills and instructions. Accounts and API keys are not included.")
	return nil
}

func (a *app) doImport() error {
	var b []byte
	err := a.unlocked(func() error {
		path, err := zenity.SelectFile(zenity.Title("Import CD-Switcher setup"), zenity.FileFilters{{Name: "CD-Switcher setup", Patterns: []string{"*.json"}}})
		if err != nil {
			return err
		}
		b, err = os.ReadFile(path)
		return err
	})
	if err != nil {
		return err
	}
	var in exportFile
	if json.Unmarshal(b, &in) != nil || in.Format != exportFormat {
		return errors.New("that file is not a CD-Switcher export")
	}
	added := 0
	for _, p := range in.Providers {
		if _, exists := a.cfg.FindProvider(p.ID); exists || p.ID == "" {
			continue
		}
		a.cfg.Providers = append(a.cfg.Providers, p)
		added++
	}
	edited := map[string]bool{}
	var servers []mcp.Server
	for _, s := range in.MCPServers {
		if s.Valid() != nil {
			continue
		}
		if i, ok := a.findMCP(s.Name); ok {
			a.cfg.MCPServers[i] = s
		} else {
			a.cfg.MCPServers = append(a.cfg.MCPServers, s)
		}
		edited[s.Name] = true
		servers = append(servers, s)
	}
	for _, e := range in.Instructions {
		if _, ok := a.findInstr(e.ID); !ok {
			a.cfg.Instructions = append(a.cfg.Instructions, e)
		}
	}
	if in.Terminal != "" {
		a.cfg.Terminal = in.Terminal
	}
	errs := mcp.Apply(a.desktopDir(), servers, edited, nil)
	if err := a.store.Save(a.cfg); err != nil {
		return err
	}
	if len(in.Skills) > 0 {
		msg := "Install these skills from GitHub?\n\n" + strings.Join(in.Skills, "\n")
		if a.confirm("Import skills", msg, "Install") == nil {
			for _, src := range in.Skills {
				if err := a.doSkillInstall(src); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	_ = zenity.Notify(fmt.Sprintf("Imported %d providers and %d MCP servers. Add the providers' API keys on the Providers tab.", added, len(servers)))
	return joinErrs(errs)
}

// ---------- cdswitcher:// links ----------

// handleLink asks the user about a link and, on yes, applies it. It runs on
// its own goroutine: the question must not hold up the op queue.
func (a *app) handleLink(raw string) {
	req, err := deeplink.Parse(raw)
	if err != nil {
		logf("link rejected: %v", err)
		notifyErr("Link not imported", err)
		return
	}
	logf("link received: %s", req.Kind)
	if err := zenity.Question(req.Summary(), zenity.Title("Import from link"), zenity.OKLabel("Add")); err != nil {
		return
	}
	a.queueOp(func() error {
		switch req.Kind {
		case "provider":
			return a.doProviderSave(req.Provider, req.Key)
		case "mcp":
			for _, s := range req.Servers {
				old := ""
				if _, ok := a.findMCP(s.Name); ok {
					old = s.Name
				}
				if err := a.doMCPSave(s, old); err != nil {
					return err
				}
			}
			return nil
		case "skill":
			return a.doSkillInstall(req.Skill.String())
		}
		return nil
	})
}
