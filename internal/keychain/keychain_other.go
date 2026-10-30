//go:build !darwin

package keychain

import "errors"

var errUnsupported = errors.New("no keychain on this platform")

func Supported() bool                                    { return false }
func Read(service, account string) (string, bool, error) { return "", false, errUnsupported }
func Write(service, account, data string) error          { return errUnsupported }
func Remove(service, account string) error               { return errUnsupported }
