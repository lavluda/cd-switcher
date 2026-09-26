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
or open, the account with the most session room left. On macOS, Settings can
launch CD-Switcher at login.

Settings notifies you once when a session or weekly limit reaches 80% (or
70% or 90%). The notice stays quiet until that window resets, or until usage
falls back under the line. Choose Off to silence it.

## Usage

```sh
make build      # -> bin/cd-switcher
./bin/cd-switcher
```

A tray icon appears.

- **First run** offers to capture the account you're currently signed into.
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

## Build

```sh
make build      # native (macOS/Linux/Windows — run on the target OS)
make windows    # prints the fyne-cross command; Fyne cannot cross-compile without cgo
make test       # unit tests for the snapshot/restore logic
```

The UI uses Fyne (OpenGL + cgo), so every binary is built on its target OS.
A Windows build needs [fyne-cross](https://github.com/fyne-io/fyne-cross) or a
Windows host with a C toolchain. `make windows` only prints that command.

## Safety notes

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
- A Developer ID signature and notarization run on release when
  `APPLE_CERTIFICATE_P12`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_NOTARY_KEY`,
  `APPLE_NOTARY_KEY_ID`, and `APPLE_NOTARY_ISSUER_ID` are set on the repo.
  Without them the disk image stays ad-hoc signed.
