# CD-Switcher

A tiny menu-bar / system-tray app that switches the logged-in account of **Claude
Desktop** and relaunches it signed into the account you pick. Built for people who
juggle two (or more) Claude accounts and prefer the desktop app, which has no
native multi-account support.

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

### Usage stats (macOS only)

Each profile shows a compact usage line — e.g. `42% of 5-hr limit · resets
3:00pm` — under its entry in the tray dropdown and in **Settings…**, refreshed
every few minutes and whenever Settings is opened. This is best-effort: only
the **active** account's stored token is guaranteed fresh, so inactive
profiles may show `—` until you switch to them. Currently macOS-only; Windows
and Linux show `—` for now.

The first fetch triggers a one-time macOS Keychain prompt ("cd-switcher wants
to access Claude Safe Storage") — approving it grants persistent read access
for future fetches. To revoke: open **Keychain Access.app**, search "Claude
Safe Storage", open the generic-password item's **Access Control** tab, and
remove cd-switcher (or re-enable "Confirm before allowing access"). After
revoking, usage stats fail soft to `—` until re-approved.

## Build

```sh
make build      # native (macOS/Linux/Windows — run on the target OS)
make windows    # cross-compile a Windows .exe from any host (no cgo)
make test       # unit tests for the snapshot/restore logic
```

The tray and macOS launch code use cgo, so native binaries are built on their
target OS. Windows uses pure-Go syscalls and cross-compiles from anywhere.

## Safety notes

- Claude must be **fully quit** before any swap; the app waits, escalates once,
  and aborts without touching files if it can't exit.
- Every restore backs up the live directory first and rolls back on error.
- Caches (`Cache`, `GPUCache`, `blob_storage`, `sentry`, …) and the local sandbox
  VM image (`vm_bundles`, multiple GB) are never copied — Claude regenerates them,
  and copying them per profile would bloat snapshots and risk filling the disk.
- Account labels are entered by you — emails live only inside encrypted blobs.
- Usage stats (macOS only) decrypt a profile's cached OAuth token transiently, in
  memory, solely to call Anthropic's own usage API for display — the token is
  never written to disk or logged. Fetches are best-effort and infrequent (every
  few minutes) since the endpoint is undocumented and may change without notice.

## Status / roadmap

- v1 swaps a single live instance. Running both accounts **simultaneously** via
  Electron `--user-data-dir` is an unverified future spike.
- MCP / extension config currently travels with each profile; making it shared
  across accounts is a possible refinement.
- Usage stats are macOS-only for now; Windows (DPAPI) and Linux (libsecret) are
  possible future additions.
