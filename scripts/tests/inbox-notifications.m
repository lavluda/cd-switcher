// Run with: sh scripts/test-inbox-notifications.sh
#import "../../inbox_notify_darwin.m"
#include <assert.h>

static NSString *opened;
void goOpenInboxApp(char *bundle) { opened = [NSString stringWithUTF8String:bundle]; }

// Notification responses have no public initializer; supply their read-only fields.
@interface CDSTestNotification : NSObject
@property UNNotificationRequest *request;
@end
@implementation CDSTestNotification
@end

@interface CDSTestResponse : NSObject
@property NSString *actionIdentifier;
@property CDSTestNotification *notification;
@end
@implementation CDSTestResponse
@end

static void checkClick(NSString *action, NSDictionary *info, NSString *expected) {
    UNMutableNotificationContent *content = cdsInboxContent(@"Finished", @"unused");
    content.userInfo = info;
    CDSTestNotification *notification = [CDSTestNotification new];
    notification.request = [UNNotificationRequest requestWithIdentifier:@"test" content:content trigger:nil];
    CDSTestResponse *response = [CDSTestResponse new];
    response.actionIdentifier = action;
    response.notification = notification;
    opened = nil;
    __block int completed = 0;
    id center = [NSObject new];
    [[CDSInboxNotifications new] userNotificationCenter:center
        didReceiveNotificationResponse:(UNNotificationResponse *)response
        withCompletionHandler:^{ completed++; }];
    assert(completed == 1);
    assert(expected == nil ? opened == nil : [opened isEqualToString:expected]);
}

int main(void) {
    @autoreleasepool {
        NSString *claude = @"com.anthropic.claudefordesktop";
        UNMutableNotificationContent *first = cdsInboxContent(@"First finished", claude);
        UNMutableNotificationContent *second = cdsInboxContent(@"Second finished", @"com.apple.Terminal");
        assert([first.title isEqualToString:@"Agent inbox"]);
        assert([first.body isEqualToString:@"First finished"]);
        checkClick(UNNotificationDefaultActionIdentifier, second.userInfo, @"com.apple.Terminal");
        checkClick(UNNotificationDefaultActionIdentifier, first.userInfo, claude);
        checkClick(UNNotificationDismissActionIdentifier, first.userInfo, nil);
        checkClick(UNNotificationDefaultActionIdentifier, @{}, nil);
        checkClick(UNNotificationDefaultActionIdentifier, @{@"cdsInboxApp": @""}, nil);
        checkClick(UNNotificationDefaultActionIdentifier, @{@"cdsInboxApp": @42}, nil);
        __block BOOL presented = NO;
        id center = [NSObject new];
        id notification = [CDSTestNotification new];
        [[CDSInboxNotifications new] userNotificationCenter:center willPresentNotification:notification
            withCompletionHandler:^(UNNotificationPresentationOptions options) {
                presented = (options & UNNotificationPresentationOptionBanner) != 0;
            }];
        assert(presented);
        // Bare binaries retain Fyne's fallback and must not call the native center.
        cdsRegisterInboxNotifications();
        assert(!cdsSendInboxNotification("Finished", [claude UTF8String]));
    }
    return 0;
}
