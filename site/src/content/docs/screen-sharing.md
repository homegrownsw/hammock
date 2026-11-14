---
title: Screen sharing
description: See and control your Mac's whole screen from your iPhone or another of your Macs.
---

Screen sharing shows a Mac's whole screen on your iPhone or on another of your Macs, and lets you click and type on it. It's for the things only the Mac's own windows can do while you're away from it: a macOS prompt an agent is stuck behind, a Keychain dialog, an app with no command line.

It's off until you turn it on, on each Mac whose screen you want to see.

## Turning it on

On the Mac you want to see:

1. Open Hammock ▸ Settings ▸ Devices, and find **Screen Sharing**.
2. Turn on **Let your iPhone and other Macs see and control this Mac's screen**.
3. Two steps appear. Click **Allow…** beside **Allow Accessibility**, and allow it in System Settings.
4. Then click **Allow…** beside **Allow Screen Recording**, and allow that too. macOS may quit and reopen the screen-sharing helper afterwards.

Each step shows a tick once it's allowed.

You can also turn it on in the [config file](/hammock/docs/configuration/), `~/.config/hammock/config`:

```
screen-sharing = true
```

You still need to allow the two permissions in Settings. Because anything running as you can write that file, an agent included, Hammock tells you when screen sharing is turned on there rather than in Settings: it adds an item to Needs You (a notification on your iPhone, if you're away) and an entry to the audit log.

## The permissions it needs

Screen sharing runs in a small helper inside Hammock, which needs two macOS permissions:

- **Screen & System Audio Recording**, so your devices can see the screen. macOS lists it as **Hammock**.
- **Accessibility**, so clicks and typing from your devices reach the Mac. macOS lists it as **Hammock Screen Sharing**.

Until both are allowed, your devices can't show the screen, and say which one is missing. See [Permissions](/hammock/docs/permissions/#accessibility-and-screen--system-audio-recording) for more.

## Viewing from your iPhone

Your iPhone must be [paired](/hammock/docs/remote/) with the Mac.

1. Open the **Devices** tab. Under **Show Screen** there's a card for each Mac whose screen you can show.
2. Tap a Mac's card. Its screen opens full screen; it's best in landscape.

If a Mac doesn't appear under Show Screen, check that screen sharing is on there and both permissions are allowed.

The iPhone has two ways to control the Mac:

- **Trackpad** (the default): drag to move the pointer, tap to click, two fingers to scroll or right-click, pinch to zoom. It's precise on small targets.
- **Touch**: tap where you mean to click. Pinch still zooms.

The controls also have a keyboard, a display picker when the Mac has more than one, and a button that pastes your iPhone's clipboard to the Mac, typed as text. The Mac's clipboard doesn't come back to the iPhone. Tap the red **×** to end the session.

## Viewing from another Mac

The Mac you view from must be [linked](/hammock/docs/remote/) with the one you want to see. Then, from any of these:

- View ▸ Show Screen ▸ the Mac's name;
- the command palette: **Show Screen of** the Mac's name;
- right-click the Mac in the sidebar's footer, and choose **Show Screen of** its name.

The screen opens in a window of its own, which you can make full screen. Clicks, scrolling and keys go to the other Mac as its own, keyboard shortcuts included while the window is in front. The bar at the top has:

- a display picker, when the Mac has more than one;
- **Fit**, to fit the screen to the window, or **1:1**, to show it at its own size;
- **End**, to stop showing the screen.

Linux computers have no screen to share, so they aren't listed.

## Watching and controlling

There's no separate view-only mode: anyone viewing can also click and type. Several of your devices can view the same screen at once, and whichever gave input last is in control.

## When the Mac is locked

Screen sharing works at the lock screen while you're logged in, and keeps the display awake while someone is watching. To unlock, type your password yourself: on the iPhone, tap the password field and use the keyboard. Hammock doesn't store it.

After the Mac restarts, nobody is logged in and Hammock isn't running yet, so log in at the Mac first.

## What the person at the Mac sees

Hammock's menu bar item shows when the screen is being shared. While anyone is viewing, its icon is filled in, with the number of viewers beside it. Its menu has a **Sharing this Mac's screen** section listing each viewer: the device's name, whether it's watching or controlling, and for how long. Each has an **End** button, and with more than one viewer, **End All Sessions** ends them all.

If you'd rather not see the menu bar item the rest of the time, set Settings ▸ General ▸ Menu Bar ▸ **Show Hammock in the menu bar** to **When a screen is shared or the server is down**, or in the config file:

```
menu-bar = active
```

It then appears whenever someone is viewing the screen.

## Networks

Screen sharing travels over the same connection as the rest of Hammock between your devices: [remote access](/hammock/docs/remote/), over Tailscale or the local networks you've chosen for it. There's nothing extra to set up for it.

## Who can see your screen

- **Only your own devices:** iPhones paired with the Mac, and your linked computers. People you've shared a Group with can't, and neither can agents, even ones running on the Mac itself.
- **Only Hammock's own server can use the helper.** The helper holds the Screen Recording and Accessibility permissions, so it serves only the Hammock server that started it, and checks that it's really that server before serving anything. A program in a pane can't use the helper to see or control your screen.
- Each viewing session is recorded in the audit log.

For the technical details, see [SECURITY.md](https://github.com/homegrownsw/hammock/blob/main/SECURITY.md).

## Turning it off

Turn off **Let your iPhone and other Macs see and control this Mac's screen** in Settings ▸ Devices ▸ Screen Sharing, or remove `screen-sharing = true` from the config file. Anyone viewing the screen is disconnected within a few seconds.

To end one session without turning screen sharing off, use **End** in the menu bar item's menu.

To take the permissions back as well, turn off Hammock in System Settings ▸ Privacy & Security ▸ Screen & System Audio Recording, and Hammock Screen Sharing in System Settings ▸ Privacy & Security ▸ Accessibility.
