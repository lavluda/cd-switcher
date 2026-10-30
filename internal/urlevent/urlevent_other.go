//go:build !darwin

// Package urlevent receives cdswitcher:// links. Only macOS delivers them to
// a running app; elsewhere the link arrives as a command-line argument.
package urlevent

// Listen returns a channel that never receives.
func Listen() <-chan string { return make(chan string) }
