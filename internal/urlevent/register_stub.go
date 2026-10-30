//go:build !darwin && !linux && !windows

package urlevent

// Register is not implemented on this platform.
func Register(exe string) error { return nil }
