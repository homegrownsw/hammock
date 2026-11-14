---
title: Permissions
description: The macOS permissions Hammock asks for, what each one is for, and how to grant or take it back.
---

Hammock asks macOS for a few permissions, each for one feature, and only when you use it. None of them is needed to run terminals and agents. This page lists each one: what it's for, who asks, when, and what happens without it.

Every permission below can be changed later in System Settings. Turning one off takes effect the next time Hammock needs it; for some, macOS asks you to quit and reopen Hammock.

## Full Disk Access

**What it's for.** Programs in your panes. macOS protects some of your files and other apps' data, and it decides whether a program may reach them by the app it runs in. For everything in a pane, that's Hammock. Without Full Disk Access, macOS can quietly stop programs in your panes from reading your files and other apps' data, sometimes without showing a prompt at all. For example, a password manager's command-line tool can fail to reach its desktop app.

**Who asks.** Hammock suggests it, but never requires it. macOS has no prompt for Full Disk Access: you add Hammock to the list yourself.

**When.** During setup, on its Full Disk Access step. You can skip it.

**What it covers.** Everything you run in Hammock gets it too, agents included. That's how other terminals work, but it's worth knowing before you turn it on.

**Granting it.**

1. Open System Settings ▸ Privacy & Security ▸ Full Disk Access. Setup's button, Settings ▸ General's **Open System Settings…** button and the command palette's **Full Disk Access…** all open it, with a small window holding Hammock's icon beside it.
2. Drag Hammock's icon into the list.
3. Choose **Quit & Reopen** when macOS asks. If you were in setup, it carries on where you left off.

**Checking it.** Settings ▸ General ▸ Privacy shows **Full Disk Access for programs in panes** as On or Off.

**Revoking it.** Turn Hammock off in System Settings ▸ Privacy & Security ▸ Full Disk Access.

## Programs in your panes

macOS treats everything running in a pane as part of Hammock, the app it runs in. So when a program in a pane wants something macOS protects, the prompt names Hammock, not the program, and what you choose applies to Hammock: to every program in every pane, agents included.

To see or change those choices, look for Hammock in the matching list in System Settings ▸ Privacy & Security. [Full Disk Access](#full-disk-access) covers most of what command-line tools need from your files.

## Notifications

**What it's for.** macOS notifications when an agent needs your input, asks for permission, finishes or hits an error.

**Who asks.** Hammock.

**When.** The first time Hammock has a notification to show as a macOS notification, so the prompt comes when an agent first needs you rather than at launch.

**Without it.** Choose **In the Hammock window** in Settings ▸ Notifications ▸ Show notifications, and notifications appear in Hammock's window instead, with no macOS permission needed. Settings ▸ Notifications shows whether macOS allows them.

**Granting or revoking it.** System Settings ▸ Notifications ▸ Hammock, or the **Open System Settings** button in Settings ▸ Notifications.

## Microphone

**What it's for.** Dictating messages to your agents in the agent view, by clicking the microphone. The speech is turned into text on your Mac.

**Who asks.** Hammock.

**When.** The first time you dictate.

**Without it.** Dictation doesn't start. Hammock says "Microphone access is off for Hammock." with an **Open Settings** button. Everything else works.

**Granting or revoking it.** System Settings ▸ Privacy & Security ▸ Microphone.

## Local Network

**What it's for.** Reaching your other computers and your iPhone over your local network: [remote access](/hammock/docs/remote/) on a local network you've chosen, and finding Hammock on your other computers there with Bonjour.

**Who asks.** Hammock.

**When.** The first time Hammock uses your local network, for example after you choose a local network for remote access in Settings ▸ Devices ▸ Remote Access.

**Without it.** Hammock can't connect to your other computers on your local network, or find them there.

**Granting or revoking it.** System Settings ▸ Privacy & Security ▸ Local Network.

## Accessibility and Screen & System Audio Recording

**What they're for.** [Screen sharing](/hammock/docs/screen-sharing/): letting your iPhone and your other Macs see this Mac's screen (Screen Recording) and send it clicks and typing (Accessibility). Nothing else in Hammock uses them. Browser and simulator panes don't need Screen Recording.

**Who asks.** Hammock's screen-sharing helper, which runs inside Hammock. macOS lists it under two names:

- in **Accessibility**, as **Hammock Screen Sharing**;
- in **Screen & System Audio Recording**, as **Hammock**.

**When.** Only once you turn on screen sharing in Settings ▸ Devices ▸ Screen Sharing, and click **Allow…** beside each step. Allow Accessibility first: macOS has the helper quit and reopen once Screen Recording is allowed.

**Without them.** Your devices can't show this Mac's screen. They say which permission is missing.

**Granting or revoking them.** System Settings ▸ Privacy & Security ▸ Accessibility, and System Settings ▸ Privacy & Security ▸ Screen & System Audio Recording. Settings ▸ Devices ▸ Screen Sharing shows a tick beside each one that's allowed.

## Login Items

**What it's for.** Keeping your terminals and agents running when Hammock isn't open. The server, which runs your panes, starts when you log in, and so does the menu bar item.

**Who asks.** Hammock registers them. macOS may ask you to approve them.

**When.** During setup, on its **Keep Hammock running** step, or when you turn on **Start the server at login** in Settings ▸ General. If macOS wants your approval, Hammock opens Login Items for you.

**Without it.** The server starts when you open Hammock, not when you log in.

**Granting or revoking it.** System Settings ▸ General ▸ Login Items & Extensions: turn on Hammock. To stop it starting at login, turn off **Start the server at login** in Settings ▸ General.

:::note
Hammock can only start at login from your Applications folder. If you open it from somewhere else, move it to Applications first.
:::

## Touch ID and the Hammock Key

The [Hammock Key](/hammock/docs/remote/), an SSH key kept in your Mac's Secure Enclave, signs only when you approve. It asks with Touch ID, or your password, in a panel that says what it will sign. This isn't a privacy permission, so there's nothing to grant in System Settings.

## On your iPhone

Hammock on the iPhone asks for:

- **Camera**, to scan the pairing code shown on your Mac.
- **Local Network**, to connect to Hammock on your Mac over your local network or a VPN.
- **Notifications**, for agents that need you.
- **Microphone**, to dictate messages to your agents when you tap the microphone.
- **Photos** (add only), to save simulator screenshots to your photos.
- **Face ID**, if you have the app ask for it when you come back to Hammock (its Settings tab, under Privacy).

Change any of them in the Settings app ▸ Apps ▸ Hammock. See [Using the iPhone app](/hammock/docs/iphone/).
