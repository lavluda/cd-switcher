//go:build darwin

// Package dockicon hides the Dock icon for menu-bar-only apps like
// CD-Switcher. The release .app bundle already sets LSUIElement in its
// Info.plist, but a plain `go build` binary (e.g. local dev via `make run`)
// has no bundle for that key to live in, so it still shows up in the Dock.
// Setting the activation policy at runtime covers both cases.
package dockicon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void hideDockIcon(void) {
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}
*/
import "C"

// Hide removes the app's Dock icon and app-switcher entry. Call it once,
// early on the main thread, before the event loop starts.
func Hide() {
	C.hideDockIcon()
}
