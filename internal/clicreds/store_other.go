//go:build !darwin

package clicreds

// Supported reports whether this platform can swap the CLI login: here it
// needs StoreDir for the saved copies.
func Supported() bool { return StoreDir != "" }

// Login storage goes through these so tests can use a fake store.
var (
	read   = fileRead
	write  = fileWrite
	remove = fileRemove
)
