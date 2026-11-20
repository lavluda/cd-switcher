//go:build !darwin

package main

func initInboxNotifications() {}

func sendInboxNotification(body, app string) bool { return false }
