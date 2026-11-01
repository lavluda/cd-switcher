# CD-Switcher

A tiny menu-bar / system-tray app that switches the logged-in account of **Claude
Desktop** and relaunches it signed into the account you pick. Built for people who
juggle two (or more) Claude accounts and prefer the desktop app, which has no
native multi-account support.

## Install

On macOS, with [Homebrew](https://brew.sh):

```sh
brew install --cask lavluda/tap/cd-switcher
```

Homebrew asks you to trust `lavluda/tap` the first time you install from it. That installs `CD-Switcher.app`. Launch it from Spotlight, or run `open -a CD-Switcher`.

To build from source instead, see [Build](#build).

## How it works

Claude Desktop stores a single logged-in identity in one data directory:

| OS | Data directory |
|----|----------------|
| macOS | `~/Library/Application Support/Claude` |
| Windows | `%APPDATA%\Claude` |
| Linux | `~/.config/Claude` |

The account lives in `Cookies` (the `.claude.ai` `sessionKey`), `config.json`
(`oauth:tokenCache*`), and the local web-storage dirs. Those secrets are encrypted
with a **per-app OS key** (macOS Keychain / Windows DPAPI / Linux libsecret) that's
identical across profiles on the same machine — so **switching accounts** swaps the
files wholesale and never decrypts anything.

Fetching usage stats (below) is the one exception: it transiently decrypts a
profile's cached OAuth token, in memory, to call Claude's own usage API.

A **profile** is a snapshot of that data directory (minus pure caches). Switching:

1. Quit Claude Desktop and wait until it has fully exited (file locks).
2. Snapshot the current account back into its profile (preserves local state).
3. Swap the target profile's snapshot into the live directory (with rollback on
   failure).
4. Relaunch Claude Desktop.

Snapshots and config live under `<user-config-dir>/cd-switcher/`.

MCP servers and extensions (`claude_desktop_config.json`, `Claude Extensions`,
and the extension install lists) stay in the live data directory. Switching
accounts does not replace them, and side-by-side windows symlink those files
back to the same copies.

## Modes

**Switch accounts** (the default) is the flow above: one Claude window, and
picking an account quits, swaps, and relaunches.

**Side by side** quits that window and opens each account with its own
`--user-data-dir`, so more than one Claude window can stay open. Turn it on
in Settings. Claude has to accept a second process; if a window does not stay
open, switch back to one account.

Control-Option-S opens the next account. The tray menu can also switch to,
or open, the account with the most session room left.

## Settings

General, Manage Profiles and Accounts cover Claude Desktop; the tabs for the
other tools are described under [Other AI tools](#other-ai-tools).

**General** — switching mode, limit alerts, open at login (macOS), automatic
update checks, the agent inbox, the terminal app, and export/import. Limit alerts notify you once when a session or
weekly limit reaches 80% (or 70% or 90%); the notice stays quiet until that
window resets, or until usage falls back under the line. Choose Off to
silence it.

**Manage Profiles** — two toggles:

- **Show Multiple Profiles** draws one compact colored swatch per profile in
  the menu bar, with a ring around the active one, instead of a single app
  icon. Takes effect with more than one profile.
- **Enable Auto-Switch** switches away from the active account the moment its
  session usage reaches the chosen percent, to the account with the most room
  left or, if you prefer, the one whose weekly limit resets soonest (so that
  allowance is spent before it expires). "Skip in auto-switch" on an account
  card keeps an account out of it. It fires once per limit window, does
  nothing in side-by-side mode (where every account is already open), and
  needs at least two profiles.

**Accounts** — every captured account with its live plan meters, a chart of
the last day of session usage on a 0-100% axis, and the switch, reorder,
rename, and remove actions.

## Other AI tools

CD-Switcher also looks after the command-line tools you use next to Claude
Desktop. Each one has a tab in Settings.

**Usage.** Today, the last 7 or the last 30 days of Claude Code and Codex
use, read from the session logs both tools keep on this machine: estimated
cost at API list prices, requests, tokens and cache hits, a chart by hour or
day (click a figure to chart it), and tables by model and by project. The logs
are only read; nothing leaves the machine.

**Claude Code login.** "Switch Claude Code login too" (Switching) gives
each account its own CLI login: the Keychain item on macOS,
`~/.claude/.credentials.json` on Linux and Windows. Only the account keys
move; MCP sign-ins and plugin secrets stay put. The switch waits for Claude Code's own locks, so it
never races a token refresh. Quit open `claude` sessions before switching.

**Claude Code limits.** "Read limits from Claude Code" (Switching) makes
CD-Switcher Claude Code's statusline command. Claude Code reports each
account's session and weekly limits there after every reply; CD-Switcher
records them and then runs the statusline you already had, so nothing
changes on screen. Meters then stay current without polling Anthropic.

**Codex & Copilot.** Save the Codex login you use now, or sign in to another
ChatGPT account (Codex opens your browser). Switching swaps
`~/.codex/auth.json` under Codex's own lock, or the Keychain item when Codex
is set to keep its login there; restart running `codex` sessions
afterwards. Each account shows its session and weekly limits, and
auto-switch moves Codex to the account with the most room too. When a GitHub
Copilot editor plugin is signed in on this Mac, its premium-request
allowance shows up as well.

**Providers.** Point Claude Code or Codex at another model API: OpenRouter,
Bedrock, Vertex, Microsoft Foundry, a local Ollama, LM Studio, llama.cpp or vLLM server, or any
compatible endpoint. Only the endpoint, key and model settings change, in
`~/.claude/settings.json` (Claude Code picks this up live) or
`~/.codex/config.toml` (next Codex start). Switching back to Official puts
back what was there before. The tab also lists shell variables such as
`ANTHROPIC_BASE_URL` that would override the choice, with a button to comment
the line out, and four Claude Code switches: hide Claude attribution, tool
search, auto-update, and the artifact tool.

**MCP.** One list of MCP servers, written into Claude Code, Claude Desktop,
Codex, Cursor, OpenCode and Gemini CLI in each one's own format. On first
start CD-Switcher reads in the servers those tools already have. Tick a tool
to add a server there; untick to remove it. An entry is rewritten only when
you edit it, so tool-specific options in it survive.

**Skills.** Install skills from a GitHub path (for example
`anthropics/skills/skills/pdf`), a zip or a local folder. They go to `~/.agents/skills`,
which Codex, Gemini CLI, Cursor, OpenCode and Qwen Code read, and are linked
into `~/.claude/skills` for Claude Code. "Share with every tool" does the same
for a skill that only Claude Code had.

**Instructions.** One set of global instructions written to `CLAUDE.md`,
`AGENTS.md`, `GEMINI.md` and `QWEN.md` for the tools you tick. Your existing
files are read in on first start; nothing is written until you choose Use.

**Agent inbox.** Turn it on in General and the menu lists your Claude Code
and Codex sessions: working, needs you, or done. You get one notification
when a session asks for permission and one when it finishes. On macOS,
clicking a session brings forward the app it runs in. It works by adding a
small hook for a few events to `~/.claude/settings.json` and
`~/.codex/hooks.json`; your other hooks stay as they are, and turning the
inbox off removes only CD-Switcher's. Codex asks you to trust new hooks
once: run `/hooks` in Codex after turning the inbox on.

**Terminal and links.** "Open Claude Code" and "Open Codex" in the menu start
the CLI in your terminal app (General). Export writes providers, MCP servers,
skills and instructions to a file for another Mac; accounts and API keys are
never exported. `cdswitcher://v1/import?resource=provider|mcp|skill` links
add a provider, MCP servers or a skill after you confirm what they contain.
On Linux and Windows the app registers the link type for your user when it
starts.

## Usage

```sh
make build      # -> bin/cd-switcher
./bin/cd-switcher
```

A tray icon appears.

- **First run** offers to capture the account you're currently signed into,
  and (on macOS) asks once whether to open CD-Switcher at login. Settings can
  change that answer later.
- **Add account…** quits Claude, saves the current account, and opens a fresh
  login. Sign into the other account, then choose **Save this account**.
- Click any profile in the menu to switch to it.

### Usage stats

Each profile shows a compact usage line — e.g. `42% of session · resets
3:00pm` — under its entry in the tray dropdown. **Settings…** shows every
limit window Claude returns (session, week, and model windows such as Opus),
extra usage as real spend, and token totals when the usage response includes
them. Claude's plan API reports how full each limit is, not a token ledger,
so a percentage is the real reading unless token counts are present. Stats
refresh every few minutes and whenever Settings is opened. This is
best-effort: only the **active** account's stored token is guaranteed fresh,
so inactive profiles may show `—` until a refresh succeeds. Windows uses
DPAPI and Linux uses libsecret for the same decrypt.

The first fetch triggers a one-time macOS Keychain prompt ("cd-switcher wants
to access Claude Safe Storage") — approving it grants persistent read access
for future fetches. To revoke: open **Keychain Access.app**, search "Claude
Safe Storage", open the generic-password item's **Access Control** tab, and
remove cd-switcher (or re-enable "Confirm before allowing access"). After
revoking, usage stats fail soft to `—` until re-approved.

## Updates

CD-Switcher checks GitHub once a day for a newer release and shows **Update
available: vX.Y.Z** in the tray menu when there is one, opening the release
page when clicked. **Check for Updates** runs one on demand, and the General
tab turns the daily check off. The check only reads GitHub's public release
metadata — it downloads and installs nothing, because `brew upgrade` and the
releases page already do that.

Released binaries are stamped with their tag at build time. A local `make
build` reports its version as `dev` and never claims to be out of date.

## Build

```sh
make build      # native (macOS/Linux/Windows — run on the target OS)
make windows    # prints the fyne-cross command; Fyne cannot cross-compile without cgo
make test       # unit tests (snapshot/restore, usage parsing, icons, update checks)
```

The UI uses Fyne (OpenGL + cgo), so every binary is built on its target OS.
A Windows build needs [fyne-cross](https://github.com/fyne-io/fyne-cross) or a
Windows host with a C toolchain. `make windows` only prints that command.

## Safety notes

- Every file CD-Switcher edits for another tool is parsed first and left
  alone if it does not parse. Only the keys it was asked to change are
  written; key order, comments in TOML, and symlinked dotfiles are kept. The
  first time it changes a file, the original is copied to
  `<user-config-dir>/cd-switcher/backups/first-write/`.
- Provider API keys are kept in an owner-only file next to the config, and
  written owner-only into the tool's own settings when that provider is in
  use.

- Claude must be **fully quit** before any swap; the app waits, escalates once,
  and aborts without touching files if it can't exit.
- Every restore backs up the live directory first and rolls back on error.
- Caches (`Cache`, `GPUCache`, `blob_storage`, `sentry`, …) and the local sandbox
  VM image (`vm_bundles`, multiple GB) are never copied — Claude regenerates them,
  and copying them per profile would bloat snapshots and risk filling the disk.
- Account labels are entered by you — emails live only inside encrypted blobs.
- Usage stats decrypt a profile's cached OAuth token in memory to call
  Anthropic's usage API. On macOS the key comes from the Keychain; on Windows
  from DPAPI; on Linux from libsecret. When an idle account's token is near
  expiry, a refresh grant is requested and, if the server rotates the refresh
  token, that new grant is written back into the profile's encrypted
  `config.json`. The access token itself is not logged. Fetches stay infrequent
  (every few minutes) because the endpoint is undocumented.

## Status / roadmap

- Switch accounts is the default. Side by side is the other mode in Settings.
- Update notifications work from v0.2.0 on: earlier binaries were built
  without a stamped version, so they never report themselves as behind.
- A Developer ID signature and notarization run on release when
  `APPLE_CERTIFICATE_P12`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_NOTARY_KEY`,
  `APPLE_NOTARY_KEY_ID`, and `APPLE_NOTARY_ISSUER_ID` are set on the repo.
  Without them the disk image stays ad-hoc signed.
