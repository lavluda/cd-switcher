//go:build !darwin

// Package clicreds swaps the Claude Code (CLI) login per account. Only macOS
// is implemented; elsewhere every call is a no-op.
package clicreds

func Supported() bool                 { return false }
func Save(id string) error            { return nil }
func Restore(id string) (bool, error) { return false, nil }
func Forget(id string) error          { return nil }
