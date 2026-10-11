//go:build !darwin

package dockicon

// Hide is a no-op off macOS; other platforms don't have a Dock.
func Hide() {}
