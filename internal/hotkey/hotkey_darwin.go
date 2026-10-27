//go:build darwin

package hotkey

/*
#cgo LDFLAGS: -framework Carbon
#include <Carbon/Carbon.h>

extern void goHotkeyFired(int);

static OSStatus hotkeyHandler(EventHandlerCallRef next, EventRef event, void *data) {
	(void)next;
	(void)data;
	EventHotKeyID hid;
	if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(hid), NULL, &hid) == noErr) {
		goHotkeyFired((int)hid.id);
	}
	return noErr;
}

// Control-Option-S is id 0; Control-Option-1..9 are ids 1..9.
// The key codes are kVK_ANSI_S, then kVK_ANSI_1 through kVK_ANSI_9.
static OSStatus installSwitcherHotkeys(void) {
	EventTypeSpec spec;
	spec.eventClass = kEventClassKeyboard;
	spec.eventKind = kEventHotKeyPressed;
	OSStatus status = InstallApplicationEventHandler(&hotkeyHandler, 1, &spec, NULL, NULL);
	if (status != noErr) {
		return status;
	}
	static const UInt32 codes[10] = {0x01, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19};
	OSStatus first = noErr;
	for (UInt32 i = 0; i < 10; i++) {
		EventHotKeyID hid;
		hid.signature = 'CDSW';
		hid.id = i;
		EventHotKeyRef ref;
		// One taken combination must not stop the others from registering.
		OSStatus s = RegisterEventHotKey(codes[i], controlKey | optionKey, hid, GetApplicationEventTarget(), 0, &ref);
		if (s != noErr && first == noErr) {
			first = s;
		}
	}
	return first;
}
*/
import "C"
import "fmt"

// fired is invoked on the main thread when a hotkey is pressed.
// It must not block.
var fired func(n int)

// Listen registers Control-Option-S (n == 0, the next account) and
// Control-Option-1 through 9 (n == 1..9, that account). fn runs on the UI
// thread and must return quickly. Call Listen on the main thread before the
// app event loop starts.
func Listen(fn func(n int)) error {
	fired = fn
	if status := C.installSwitcherHotkeys(); status != C.noErr {
		return fmt.Errorf("hotkey: register Control-Option shortcuts: OSStatus %d", int(status))
	}
	return nil
}

//export goHotkeyFired
func goHotkeyFired(id C.int) {
	if fired != nil {
		fired(int(id))
	}
}
