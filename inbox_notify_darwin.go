//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdbool.h>
#include <stdlib.h>
void cdsRegisterInboxNotifications(void);
bool cdsSendInboxNotification(const char *body, const char *app);
*/
import "C"

import (
	"unsafe"

	"fyne.io/fyne/v2"
	"github.com/lavluda/cd-switcher/internal/agents"
)

func initInboxNotifications() { C.cdsRegisterInboxNotifications() }

func sendInboxNotification(body, app string) bool {
	text, bundle := C.CString(body), C.CString(app)
	defer C.free(unsafe.Pointer(text))
	defer C.free(unsafe.Pointer(bundle))
	return bool(C.cdsSendInboxNotification(text, bundle))
}

//export goOpenInboxApp
func goOpenInboxApp(bundle *C.char) {
	s := agents.Session{App: C.GoString(bundle)}
	fyne.Do(func() {
		a.hidePanel()
		a.openSession(s)
	})
}
