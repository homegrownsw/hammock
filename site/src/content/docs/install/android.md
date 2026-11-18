---
title: Install on Android
description: Build the Android app, pair it with your Mac and turn on notifications.
---

Hammock for Android shows the workspace on your Mac (or a Linux server running Hammock), live, on Android phones,
foldables and tablets. It needs **Android 13 or later**, and pairs with your own computer directly, with no account.

## Get the app

The Android app is in development and isn't on Google Play yet. To try it now, build it from the source
(`android/README.md` in the repository) and install it with `just android-device`.

## Pair with your Mac

Turn on remote access on the Mac and show its pairing code as for the iPhone ([steps 1–3](/hammock/docs/install/iphone/#pair-with-your-mac)),
then open Hammock on Android and tap **Scan Pairing Code**, or paste the code's link and tap **Pair**. Your phone has
to be on one of the [networks](/hammock/docs/install/iphone/#networks) remote access listens on.

To pair another computer, tap **Add Computer** in the **Devices** tab.

## Notifications

When an agent needs you while you're away from your Mac, the Mac sends a notification to your phone through Google's
push service, encrypted so only your phone can read it. Allow notifications when Hammock asks, then open the app once
while it's connected to your Mac. Choose which you get in **Settings ▸ Notify Me When**, and how each kind sounds
in Android's notification settings.

Next: [using the Android app](/hammock/docs/android/).
