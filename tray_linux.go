//go:build linux

package main

import "github.com/godbus/dbus/v5"

// trayHostAvailable reports whether something on the session bus can show a
// StatusNotifierItem tray icon, which is the only kind fyne/systray draws on
// Linux. Without a watcher (plain i3, sway or GNOME with no AppIndicator
// extension) the icon never appears and its click could never open the panel.
func trayHostAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var owned bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&owned)
	return err == nil && owned
}
