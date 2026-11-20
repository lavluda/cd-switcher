#!/bin/sh
set -eu
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
clang -Werror -fobjc-arc -fblocks -framework Foundation -framework UserNotifications \
    scripts/tests/inbox-notifications.m -o "$work/test-inbox-notifications"
"$work/test-inbox-notifications"
