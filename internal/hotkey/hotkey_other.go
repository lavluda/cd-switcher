//go:build !darwin

package hotkey

// Listen is a no-op off macOS.
func Listen(fn func()) error { return nil }
