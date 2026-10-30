//go:build darwin

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

extern void goHandleURL(char *);

@interface CDSURLHandler : NSObject
- (void)handle:(NSAppleEventDescriptor *)event withReply:(NSAppleEventDescriptor *)reply;
@end

@implementation CDSURLHandler
- (void)handle:(NSAppleEventDescriptor *)event withReply:(NSAppleEventDescriptor *)reply {
	NSString *url = [[event paramDescriptorForKeyword:keyDirectObject] stringValue];
	if (url != nil) {
		goHandleURL((char *)[url UTF8String]);
	}
}
@end

static CDSURLHandler *handler;

void cdsRegisterURLHandler(void) {
	if (handler == nil) {
		handler = [CDSURLHandler new];
	}
	[[NSAppleEventManager sharedAppleEventManager] setEventHandler:handler
	                                                  andSelector:@selector(handle:withReply:)
	                                                forEventClass:kInternetEventClass
	                                                   andEventID:kAEGetURL];
}
