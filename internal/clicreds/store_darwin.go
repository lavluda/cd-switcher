package clicreds

import "github.com/lavluda/cd-switcher/internal/keychain"

// Supported reports whether this platform can swap the CLI login.
func Supported() bool { return true }

// Login storage goes through these so tests can use a fake store.
var (
	read   = keychain.Read
	write  = keychain.Write
	remove = keychain.Remove
)
