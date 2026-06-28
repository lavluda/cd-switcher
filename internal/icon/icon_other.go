//go:build !windows

package icon

// Data returns the tray icon bytes. macOS and Linux trays accept PNG directly.
func Data() []byte { return pngBytes() }
