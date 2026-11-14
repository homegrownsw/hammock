---
title: Install on iPhone
description: Get the iPhone app from TestFlight, pair it with your Mac and turn on notifications.
---

Hammock for iPhone shows the workspace on your Mac (or a Linux server running Hammock): your Groups, terminals and
agents, live. It needs **iOS 26 or later**, and Hammock running on a computer of yours to pair with. There's no
account to make: your iPhone pairs with your own computer directly.

## Get the app

The iPhone app is in beta on **TestFlight**. Install TestFlight from the App Store, then Hammock from your
TestFlight invitation.

No Mac to hand yet? Tap **See a demo** on the app's first screen to try it with a made-up Mac. **Pair Your Mac**
leaves the demo.

## Pair with your Mac

Your iPhone connects to your Mac directly, over a network you choose, so the Mac has to allow it first.

1. On the Mac, open Hammock ▸ Settings ▸ Devices.
2. Turn on **Allow paired devices to connect remotely**. The first time, Hammock asks **Where should Hammock
   listen?**: choose the networks your iPhone will reach the Mac on (see [below](#networks)), then **Listen on
   These**.
3. Under **Pair a Device**, choose **Show Pairing Code**. A QR code and a link appear. Each code works once and
   expires in 10 minutes.
4. On the iPhone, open Hammock and scan the code, or paste the link under **Or paste the link** and tap **Pair**.

File ▸ Pair a Device… on the Mac shows the same code from anywhere, and offers to turn on remote access if it's off.

If you open a pairing link outside the app, Hammock asks **Pair with** *the computer*? and shows its address and
certificate. Only pair if you've just asked that computer for a code.

### More than one computer

To pair another Mac, or a Linux server, open the **Devices** tab and tap **Add Computer**. On a server, run
`hmk pair` to print the code as a QR code in the terminal ([Linux](/hammock/docs/install/linux/#pair-your-iphone)).
The iPhone shows one computer at a time: tap one in Devices to switch. Swipe one to forget it.

On the Mac, Settings ▸ Devices ▸ **Paired Devices** lists each paired iPhone and when it was last seen, with
**Revoke** to remove one. `hmk devices` does the same from a terminal.

## Networks

Remote access listens only on the networks you choose, and every device still has to be paired. Your iPhone has to
be on one of them to reach your Mac:

- **Tailscale** works from anywhere, at home or away. Install it on the Mac and the iPhone, on the same tailnet. With no network chosen, remote access listens on Tailscale only.
- **A local network**, such as your home Wi-Fi (`192.168.1.0/24`), works while the iPhone is on it.

Change them any time in Settings ▸ Devices ▸ Remote Access, or with `hmk`:

```sh
hmk remote networks                  # the networks this Mac is on; * chosen
hmk remote enable tailscale
hmk remote enable 192.168.1.0/24
hmk remote on
```

See [Remote access](/hammock/docs/remote/) for linked computers, sharing Groups and extra addresses.

## Notifications

When an agent needs you while you're away from your Mac (its screen is locked, or you haven't touched it for a
couple of minutes), the Mac sends a notification to your iPhone. They're encrypted on your Mac, and only your
iPhone can read them.

1. Allow notifications when Hammock asks, or later for Hammock in the iPhone's Settings.
2. Open the app once while it's connected to your Mac, so the Mac learns where to send them.

Choose which you get in the app's **Settings** tab, under **Notify Me When**: someone asks to join a shared Group,
an agent needs you, an agent finishes, or an agent hits an error. Its footer says whether push is on.

Next: [using the iPhone app](/hammock/docs/iphone/).
