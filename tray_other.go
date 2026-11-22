//go:build !linux

package main

// trayHostAvailable: macOS and Windows always have a menu bar / notification area.
func trayHostAvailable() bool { return true }
