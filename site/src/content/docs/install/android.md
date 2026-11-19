---
title: Install on Android
description: Install the Android app, pair it with your Mac and turn on notifications.
---

Hammock for Android shows the workspace on your Mac (or a Linux server running Hammock), live, on Android phones,
foldables and tablets. It needs **Android 13 or later**, and pairs with your own computer directly, with no account.

## Get the app

Hammock for Android isn't on Google Play: it comes as an APK. On your phone, open the newest
[Android release](https://github.com/homegrownsw/hammock/releases?q=Android-v&expanded=true) and download
`Hammock-<version>.apk`, then open it. The first time, Android asks you to let your browser install apps:
allow it, then go back and install.

Hammock checks for a new version once a day and offers it; **Settings ▸ Check for Updates** looks now. Install
an update the same way, over the app you have: your computers and settings stay.

Every release is signed with the same key. To check an APK, `apksigner verify --print-certs` shows its
certificate's SHA-256 digest, which should be
`3e004932943b43d6389483e921f1b9fb9c7ff37d03f624124dad0db9ce99671b`.

## Pair with your Mac

1. On the Mac, open Hammock ▸ Settings ▸ Devices.
2. Turn on **Allow paired devices to connect remotely**, and choose the networks your phone will reach the Mac on
   (see [below](#networks)).
3. Under **Pair a Device**, choose **Show Pairing Code** (or File ▸ Pair a Device…). A code works once, for 10
   minutes.
4. On the phone, open Hammock and tap **Scan Pairing Code**, or paste the code's link and tap **Pair**.

If you open a pairing link outside the app, only pair if you've just asked that computer for a code.

### More than one computer

To pair another Mac, or a Linux server, tap **Add Computer** in the **Devices** tab. On a server, `hmk pair` prints
the code in the terminal ([Linux](/hammock/docs/install/linux/#pair-your-phone)). Revoke a paired phone in the
Mac's Settings ▸ Devices ▸ **Paired Devices**.

## Networks

Your phone reaches your Mac only on the networks you chose in step 2:

- **Tailscale** works from anywhere. Install it on the Mac and the phone, signed in to the same tailnet.
- **A local network**, such as your home Wi-Fi, works while the phone is on it, not when you're out on mobile data.
- **Your own VPN** (WireGuard, OpenVPN, ZeroTier and the like) works while the phone is connected to it.

If the phone can't connect, check it's on one of those, with the VPN app on. [Remote access](/hammock/docs/remote/#remote-access)
has the rest: VPNs, DNS names and forwarded ports, and choosing networks with `hmk`.

## Notifications

When an agent needs you while you're away from your Mac, the Mac sends a notification to your phone through Google's
push service, encrypted so only your phone can read it. Allow notifications when Hammock asks, then open the app once
while it's connected to your Mac. Choose which you get in **Settings ▸ Notify Me When**, and how each kind sounds
in Android's notification settings.

Next: [using the Android app](/hammock/docs/android/).
