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
identical across profiles on the same machine — so CD-Switcher **swaps the files
wholesale and never decrypts anything**.

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
- Account labels are entered by you — emails live only inside encrypted blobs that
  CD-Switcher deliberately never reads.

## Status / roadmap

- v1 swaps a single live instance. Running both accounts **simultaneously** via
  Electron `--user-data-dir` is an unverified future spike.
- MCP / extension config currently travels with each profile; making it shared
  across accounts is a possible refinement.
