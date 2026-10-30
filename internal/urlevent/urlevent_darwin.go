//go:build darwin

// Package urlevent receives the cdswitcher:// links macOS hands to the app
// bundle as Apple Events.
package urlevent

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework AppKit
void cdsRegisterURLHandler(void);
*/
import "C"

var urls = make(chan string, 8)

//export goHandleURL
func goHandleURL(u *C.char) {
	select {
	case urls <- C.GoString(u):
	default: // a burst of links beyond the buffer is dropped, not queued
	}
}

// Registered at start-up so a link that launches the app is not missed.
func init() { C.cdsRegisterURLHandler() }

// Forward is for platforms that start a new process per link; macOS hands
// links to the running app itself.
func Forward(stateDir, link string) bool { return false }

// Register is done by the bundle's Info.plist on macOS.
func Register(exe string) error { return nil }

// Listen registers the handler again (AppKit may install its own while it
// finishes launching) and returns the links as they arrive.
func Listen(stateDir string) <-chan string {
	C.cdsRegisterURLHandler()
	return urls
}
