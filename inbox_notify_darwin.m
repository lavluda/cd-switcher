//go:build darwin

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

extern void goOpenInboxApp(char *bundle);

@interface CDSInboxNotifications : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation CDSInboxNotifications
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       didReceiveNotificationResponse:(UNNotificationResponse *)response
       withCompletionHandler:(void (^)(void))completionHandler {
    if ([response.actionIdentifier isEqualToString:UNNotificationDefaultActionIdentifier]) {
        id bundle = response.notification.request.content.userInfo[@"cdsInboxApp"];
        if ([bundle isKindOfClass:[NSString class]] && [bundle length] > 0) {
            goOpenInboxApp((char *)[bundle UTF8String]);
        }
    }
    completionHandler();
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
       withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
    completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList);
}
@end

// The center holds its delegate weakly. Keep it alive for clicks after relaunch, too.
static CDSInboxNotifications *inboxNotifications;

void cdsRegisterInboxNotifications(void) {
    if ([[NSBundle mainBundle] bundleIdentifier] == nil) return;
    inboxNotifications = [CDSInboxNotifications new];
    [UNUserNotificationCenter currentNotificationCenter].delegate = inboxNotifications;
}

UNMutableNotificationContent *cdsInboxContent(NSString *body, NSString *bundle) {
    UNMutableNotificationContent *content = [UNMutableNotificationContent new];
    content.title = @"Agent inbox";
    content.body = body;
    content.userInfo = @{@"cdsInboxApp": bundle};
    return content;
}

bool cdsSendInboxNotification(const char *body, const char *app) {
    if ([[NSBundle mainBundle] bundleIdentifier] == nil) return false;
    UNMutableNotificationContent *content = cdsInboxContent(
        [NSString stringWithUTF8String:body], [NSString stringWithUTF8String:app]);
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert
        completionHandler:^(BOOL granted, NSError *error) {
            if (!granted) {
                NSLog(@"Agent inbox notifications not authorized: %@", error);
                return;
            }
            UNNotificationRequest *request = [UNNotificationRequest
                requestWithIdentifier:[[NSUUID UUID] UUIDString] content:content trigger:nil];
            [center addNotificationRequest:request withCompletionHandler:^(NSError *error) {
                if (error != nil) NSLog(@"Agent inbox notification failed: %@", error);
            }];
        }];
    return true;
}
