//go:build darwin

package hotkey

/*
#cgo LDFLAGS: -framework Carbon
#include <Carbon/Carbon.h>

extern void goHotkeyFired(void);

static OSStatus hotkeyHandler(EventHandlerCallRef next, EventRef event, void *data) {
	(void)next;
	(void)event;
	(void)data;
	goHotkeyFired();
	return noErr;
}

static OSStatus installSwitcherHotkey(void) {
	EventTypeSpec spec;
	spec.eventClass = kEventClassKeyboard;
	spec.eventKind = kEventHotKeyPressed;
	OSStatus status = InstallApplicationEventHandler(&hotkeyHandler, 1, &spec, NULL, NULL);
	if (status != noErr) {
		return status;
	}
	EventHotKeyID hid;
	hid.signature = 'CDSW';
	hid.id = 1;
	EventHotKeyRef ref;
	// Control-Option-S. kVK_ANSI_S is 0x01.
	return RegisterEventHotKey(0x01, controlKey | optionKey, hid, GetApplicationEventTarget(), 0, &ref);
}
*/
import "C"
import "fmt"

// fired is invoked on the main thread when Control-Option-S is pressed.
// It must not block.
var fired func()

// Listen registers Control-Option-S. fn runs on the UI thread and must return
// quickly. Call Listen on the main thread before the app event loop starts.
func Listen(fn func()) error {
	fired = fn
	if status := C.installSwitcherHotkey(); status != C.noErr {
		return fmt.Errorf("hotkey: register Control-Option-S: OSStatus %d", int(status))
	}
	return nil
}

//export goHotkeyFired
func goHotkeyFired() {
	if fired != nil {
		fired()
	}
}
