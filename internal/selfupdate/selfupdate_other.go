//go:build !darwin

// Package selfupdate installs releases in place. Only macOS is implemented;
// elsewhere the tray falls back to opening the release page.
package selfupdate

import (
	"context"
	"errors"
)

func Supported() bool { return false }

func DMGURL(tag string) string { return "" }

func Install(ctx context.Context, url string) error {
	return errors.New("in-place update is macOS only")
}
