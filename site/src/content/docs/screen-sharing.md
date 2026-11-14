---
title: Screen sharing
description: See and control your Mac's whole screen from your iPhone or another of your Macs.
---

Screen sharing shows a Mac's whole screen on your iPhone or another of your Macs, and lets you click and type on it: for a macOS prompt an agent is stuck behind, a Keychain dialog, or an app with no command line. It goes over [remote access](/hammock/docs/remote/), with nothing extra to set up.

## Turning it on

On the Mac you want to see:

1. Open Hammock ▸ Settings ▸ Devices ▸ **Screen Sharing**.
2. Turn on **Let your iPhone and other Macs see and control this Mac's screen**.
3. Click **Allow…** beside **Allow Accessibility**, and allow it in System Settings.
4. Then click **Allow…** beside **Allow Screen Recording**, and allow that too.

Or set `screen-sharing = true` in the [config file](/hammock/docs/configuration/), and allow the two permissions in Settings. Because an agent could write that file too, Hammock tells you in Needs You when it's turned on there.

In System Settings, Screen Recording lists the helper as **Hammock**, and Accessibility as **Hammock Screen Sharing**. See [Permissions](/hammock/docs/permissions/#accessibility-and-screen--system-audio-recording).

## Viewing from your iPhone

Your iPhone must be [paired](/hammock/docs/remote/) with the Mac. In the **Devices** tab, tap the Mac under **Show Screen**. It's best in landscape. If a Mac isn't listed, check that screen sharing is on there and both permissions are allowed.

- **Trackpad** (the default): drag to move the pointer, tap to click, two fingers to scroll or right-click, pinch to zoom.
- **Touch**: tap where you mean to click.

The controls also have a keyboard, a display picker, and a button that types your iPhone's clipboard on the Mac. Tap the red **×** to end the session.

## Viewing from another Mac

The Mac you view from must be [linked](/hammock/docs/remote/) with the one you want to see. Choose View ▸ Show Screen ▸ the Mac's name, or **Show Screen of** it in the command palette or by right-clicking it in the sidebar's footer.

The screen opens in a window of its own. Clicks, scrolling and keys, shortcuts included, go to the other Mac. The bar at the top has a display picker, **Fit** or **1:1**, and **End**.

## Watching and controlling

Anyone viewing can also click and type. Several devices can view at once, and whichever gave input last is in control.

## When the Mac is locked

Screen sharing works at the lock screen while you're logged in. To unlock, type your password yourself. After a restart, log in at the Mac first.

## What the person at the Mac sees

While anyone is viewing, Hammock's menu bar icon is filled in, with the number of viewers beside it. Its menu lists each viewer with an **End** button.

To show the menu bar item only then, set Settings ▸ General ▸ Menu Bar ▸ **Show Hammock in the menu bar** to **When a screen is shared or the server is down** (`menu-bar = active`).

## Who can see your screen

- **Only your own devices:** iPhones paired with the Mac, and your linked computers. Not people you've shared a Group with, and not agents, even on the Mac itself.
- **Only Hammock's own server can use the helper**, so a program in a pane can't use it to see or control your screen.
- Each viewing session is recorded in the audit log.

For the technical details, see [SECURITY.md](https://github.com/homegrownsw/hammock/blob/main/SECURITY.md).

## Turning it off

Turn off **Let your iPhone and other Macs see and control this Mac's screen** in Settings ▸ Devices ▸ Screen Sharing, or remove `screen-sharing = true` from the config file. Anyone viewing is disconnected. To take the permissions back too, turn Hammock off in both lists in System Settings ▸ Privacy & Security.
