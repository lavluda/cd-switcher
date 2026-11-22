#!/bin/sh
# Installs CD-Switcher for the current user on Linux: the binary in
# ~/.local/bin, an app-menu entry and its icon. Nothing needs root.
#
#   sh scripts/install-linux.sh [path/to/cd-switcher] [path/to/icon.png]
#   sh scripts/install-linux.sh --uninstall
#
# Run with no arguments from an unpacked release tarball (binary and icon.png
# next to this script), or let `make install` pass the paths.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bindir="${XDG_BIN_HOME:-$HOME/.local/bin}"
data="${XDG_DATA_HOME:-$HOME/.local/share}"
apps="$data/applications"
icons="$data/icons/hicolor/32x32/apps"

if [ "${1:-}" = "--uninstall" ]; then
	rm -f "$bindir/cd-switcher" "$apps/cd-switcher.desktop" "$icons/cd-switcher.png"
	# Written by the app itself: the link handler and the login item.
	rm -f "$apps/cd-switcher-link.desktop" "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/cd-switcher.desktop"
	command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" || true
	echo "Removed CD-Switcher. Saved profiles stay in ${XDG_CONFIG_HOME:-$HOME/.config}/cd-switcher."
	exit 0
fi

bin="${1:-}"
if [ -z "$bin" ]; then
	for c in "$here/cd-switcher" "$here/cd-switcher-linux-amd64" "$here/cd-switcher-linux-arm64"; do
		[ -f "$c" ] && bin=$c && break
	done
fi
[ -n "$bin" ] && [ -f "$bin" ] || { echo "cd-switcher binary not found" >&2; exit 1; }
icon="${2:-$here/icon.png}"

mkdir -p "$bindir" "$apps" "$icons"
install -m 755 "$bin" "$bindir/cd-switcher"
[ -f "$icon" ] && install -m 644 "$icon" "$icons/cd-switcher.png"

cat >"$apps/cd-switcher.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=CD-Switcher
Comment=Switch Claude Desktop accounts
Exec="$bindir/cd-switcher"
Icon=cd-switcher
Terminal=false
Categories=Utility;
StartupNotify=false
EOF
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" || true

echo "Installed $bindir/cd-switcher. Start it from your app menu or run cd-switcher."
case ":$PATH:" in *":$bindir:"*) ;; *) echo "Note: $bindir is not on your PATH." ;; esac

# Runtime pieces the app shells out to or links against.
missing=""
command -v secret-tool >/dev/null 2>&1 || missing="$missing secret-tool (libsecret-tools: usage stats)"
command -v xdg-mime >/dev/null 2>&1 || missing="$missing, xdg-mime (xdg-utils: cdswitcher:// links)"
if command -v ldconfig >/dev/null 2>&1 && ! ldconfig -p 2>/dev/null | grep -q libayatana-appindicator3; then
	missing="$missing, libayatana-appindicator3 (tray icon)"
fi
[ -n "$missing" ] && echo "Optional, not found:${missing#, }" || true
