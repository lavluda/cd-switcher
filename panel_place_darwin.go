//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

static void place_panel(const char *title, double width, double height) {
	NSString *want = [NSString stringWithUTF8String:title];
	NSWindow *target = nil;
	for (NSWindow *w in [NSApp windows]) {
		if ([[w title] isEqualToString:want]) {
			target = w;
			break;
		}
	}
	if (target == nil) {
		return;
	}
	[target setLevel:NSPopUpMenuWindowLevel];
	[target setHidesOnDeactivate:YES];
	NSPoint p = [NSEvent mouseLocation];
	NSScreen *screen = [NSScreen mainScreen];
	for (NSScreen *s in [NSScreen screens]) {
		if (NSPointInRect(p, [s frame])) {
			screen = s;
			break;
		}
	}
	NSRect vis = [screen visibleFrame];
	if (width > NSWidth(vis) - 16.0) width = NSWidth(vis) - 16.0;
	if (height > NSHeight(vis) - 16.0) height = NSHeight(vis) - 16.0;
	CGFloat x = p.x - width / 2.0;
	CGFloat y = NSMaxY(vis) - height - 6.0;
	if (p.y < NSMaxY(vis) && p.y - height - 8.0 >= NSMinY(vis)) {
		y = p.y - height - 8.0;
	}
	if (x < NSMinX(vis) + 8.0) x = NSMinX(vis) + 8.0;
	if (x + width > NSMaxX(vis) - 8.0) x = NSMaxX(vis) - width - 8.0;
	if (y < NSMinY(vis) + 8.0) y = NSMinY(vis) + 8.0;
	target.opaque = NO;
	target.backgroundColor = [NSColor clearColor];
	target.hasShadow = YES;
	if (target.contentView != nil) {
		[target.contentView setWantsLayer:YES];
		target.contentView.layer.cornerRadius = 18.0;
		target.contentView.layer.masksToBounds = YES;
	}
	[target setFrame:NSMakeRect(x, y, width, height) display:YES];
}

static NSWindow *find_window(const char *title) {
	NSString *want = [NSString stringWithUTF8String:title];
	for (NSWindow *w in [NSApp windows]) {
		if ([[w title] isEqualToString:want]) return w;
	}
	return nil;
}

// The offset between the mouse and the window origin when a drag starts.
static NSPoint drag_offset;

static void drag_begin(const char *title) {
	NSWindow *w = find_window(title);
	if (w == nil) return;
	NSPoint p = [NSEvent mouseLocation];
	drag_offset = NSMakePoint(p.x - w.frame.origin.x, p.y - w.frame.origin.y);
}

static void drag_move(const char *title) {
	NSWindow *w = find_window(title);
	if (w == nil) return;
	NSPoint p = [NSEvent mouseLocation];
	[w setFrameOrigin:NSMakePoint(p.x - drag_offset.x, p.y - drag_offset.y)];
}

static double visible_height(void) {
	NSPoint p = [NSEvent mouseLocation];
	NSScreen *screen = [NSScreen mainScreen];
	for (NSScreen *s in [NSScreen screens]) {
		if (NSPointInRect(p, [s frame])) {
			screen = s;
			break;
		}
	}
	return NSHeight([screen visibleFrame]);
}

static int panel_visible(const char *title) {
	NSString *want = [NSString stringWithUTF8String:title];
	for (NSWindow *w in [NSApp windows]) {
		if ([[w title] isEqualToString:want]) {
			return [w isVisible] ? 1 : 0;
		}
	}
	return 0;
}
*/
import "C"
import "unsafe"

func menuPanelHeight() float32 {
	h := float32(C.visible_height()) - 16
	if h < 460 {
		h = 460
	}
	if h > panelH {
		h = panelH
	}
	return h
}

func placePanel(title string, width, height float32) {
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	C.place_panel(cs, C.double(width), C.double(height))
}

func (a *app) panelShowing() bool {
	if a.panelWin == nil {
		return false
	}
	cs := C.CString(panelTitle)
	defer C.free(unsafe.Pointer(cs))
	return C.panel_visible(cs) == 1
}

func panelDragBegin(title string) {
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	C.drag_begin(cs)
}

func panelDragMove(title string) {
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	C.drag_move(cs)
}
