// Command genicon writes the CD-Switcher icon as a PNG to the path given as the
// first argument (default "icon.png"). The icon is generated in code (see
// internal/icon), so release packaging can build the macOS .icns without the
// repo carrying any binary image asset.
package main

import (
	"os"

	"github.com/lavluda/cd-switcher/internal/icon"
)

func main() {
	out := "icon.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.WriteFile(out, icon.Data(), 0o644); err != nil {
		panic(err)
	}
}
