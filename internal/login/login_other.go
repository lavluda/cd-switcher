//go:build !darwin

package login

import "errors"

// ErrUnsupported is returned on platforms without a login-item implementation.
var ErrUnsupported = errors.New("login: open at login is only supported on macOS")

// Supported reports whether this OS can register a login item.
func Supported() bool { return false }

// Enable registers the app to launch at login.
func Enable() error { return ErrUnsupported }

// Disable removes the login item.
func Disable() error { return ErrUnsupported }
