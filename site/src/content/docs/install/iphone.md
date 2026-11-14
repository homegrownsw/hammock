---
title: Install on iPhone
description: Get the iPhone app from TestFlight, pair it with your Mac and turn on notifications.
---

Hammock for iPhone shows the workspace on your Mac (or a Linux server running Hammock), live. It needs **iOS 26 or
later**, and pairs with your own computer directly, with no account.

## Get the app

The iPhone app is in beta on **TestFlight**. Install TestFlight from the App Store, then Hammock from your
TestFlight invitation. No Mac to hand yet? Tap **See a demo** to try it with a made-up Mac.

## Pair with your Mac

1. On the Mac, open Hammock ▸ Settings ▸ Devices.
2. Turn on **Allow paired devices to connect remotely**, and choose the networks your iPhone will reach the Mac on
   (see [below](#networks)).
3. Under **Pair a Device**, choose **Show Pairing Code** (or File ▸ Pair a Device…). A code works once, for 10
   minutes.
4. On the iPhone, open Hammock and scan the code, or paste its link and tap **Pair**.

If you open a pairing link outside the app, only pair if you've just asked that computer for a code.

### More than one computer

To pair another Mac, or a Linux server, tap **Add Computer** in the **Devices** tab. On a server, `hmk pair` prints
the code in the terminal ([Linux](/hammock/docs/install/linux/#pair-your-iphone)). Revoke a paired iPhone in the
Mac's Settings ▸ Devices ▸ **Paired Devices**.

## Networks

Your iPhone has to be on one of the networks remote access listens on:

- **Tailscale** works from anywhere. Install it on the Mac and the iPhone, on the same tailnet.
- **A local network**, such as your home Wi-Fi (`192.168.1.0/24`), works while the iPhone is on it.

Change them in Settings ▸ Devices ▸ Remote Access, or with `hmk`:

```sh
hmk remote networks                  # the networks this Mac is on; * chosen
hmk remote enable tailscale
hmk remote enable 192.168.1.0/24
hmk remote on
```

See [Remote access](/hammock/docs/remote/) for linked computers, sharing Groups and extra addresses.

## Notifications

When an agent needs you while you're away from your Mac, the Mac sends a notification to your iPhone, encrypted so
only your iPhone can read it.

1. Allow notifications when Hammock asks, or later in the iPhone's Settings.
2. Open the app once while it's connected to your Mac, so the Mac learns where to send them.

Choose which you get in the app's **Settings** tab, under **Notify Me When**.

Next: [using the iPhone app](/hammock/docs/iphone/).
