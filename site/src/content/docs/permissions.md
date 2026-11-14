---
title: Permissions
description: The macOS permissions Hammock asks for, what each one is for, and how to grant or take it back.
---

Setup asks for Full Disk Access, which programs in your panes rely on for a lot. The others are each for one feature, and are asked for when you first use it. Change any of them later in System Settings ▸ Privacy & Security.

## Full Disk Access

**What it's for.** Programs in your panes. macOS decides whether a program may reach your protected files and other apps' data by the app it runs in, which for a pane is Hammock. Without Full Disk Access, programs in your panes can quietly fail to read them: a password manager's command-line tool can fail to reach its desktop app, for example.

**What it covers.** Everything you run in Hammock gets it too, agents included, as in other terminals. It's suggested, never required.

**Granting it.** Setup offers it, as do Settings ▸ General ▸ Privacy and the command palette's **Full Disk Access…**:

1. Open System Settings ▸ Privacy & Security ▸ Full Disk Access.
2. Drag Hammock's icon into the list.
3. Choose **Quit & Reopen** when macOS asks.

Settings ▸ General ▸ Privacy shows whether it's on.

## Programs in your panes

macOS treats everything in a pane as part of Hammock. When a program in a pane wants something macOS protects, the prompt names Hammock, and what you choose applies to every program in every pane, agents included. Those choices are under Hammock in System Settings ▸ Privacy & Security. [Full Disk Access](#full-disk-access) covers most of what command-line tools need.

## Notifications

**What it's for.** macOS notifications when an agent needs you, finishes or hits an error. macOS asks the first time Hammock has one to show.

**Without it.** Choose **In the Hammock window** in Settings ▸ Notifications ▸ Show notifications, and they appear in Hammock's window instead.

**Granting or revoking it.** System Settings ▸ Notifications ▸ Hammock.

## Microphone

**What it's for.** Dictating to your agents in the agent view, on your Mac. macOS asks the first time you dictate.

**Granting or revoking it.** System Settings ▸ Privacy & Security ▸ Microphone.

## Local Network

**What it's for.** [Remote access](/hammock/docs/remote/) on a local network you've chosen, and finding Hammock on your other computers there. macOS asks the first time Hammock uses your local network.

**Granting or revoking it.** System Settings ▸ Privacy & Security ▸ Local Network.

## Accessibility and Screen & System Audio Recording

**What they're for.** [Screen sharing](/hammock/docs/screen-sharing/) only: letting your iPhone and other Macs see this Mac's screen (Screen Recording) and click and type on it (Accessibility). Browser and simulator panes don't need them.

**When.** Once you turn on screen sharing in Settings ▸ Devices ▸ Screen Sharing and click **Allow…** beside each step. Allow Accessibility first.

**Granting or revoking them.** System Settings ▸ Privacy & Security ▸ Accessibility, where the helper is **Hammock Screen Sharing**, and Screen & System Audio Recording, where it's **Hammock**.

## Login Items

**What it's for.** Starting the server, which runs your panes, and the menu bar item when you log in. Without it, the server starts when you open Hammock.

**Granting or revoking it.** Setup's **Keep Hammock running** step, or **Start the server at login** in Settings ▸ General. If macOS asks, turn Hammock on in System Settings ▸ General ▸ Login Items & Extensions.

:::note
Hammock can only start at login from your Applications folder.
:::

## Touch ID and the Hammock Key

The [Hammock Key](/hammock/docs/remote/) signs only when you approve, with Touch ID or your password, in a panel that says what it will sign. There's nothing to grant in System Settings.

## On your iPhone

Hammock on the iPhone asks for:

- **Camera**, to scan the pairing code.
- **Local Network**, to reach your Mac over your local network or a VPN.
- **Notifications**, for agents that need you.
- **Microphone**, to dictate to your agents.
- **Photos** (add only), to save simulator screenshots.
- **Face ID**, if you turn on **Require Face ID** in its Settings tab.

Change any of them in the Settings app ▸ Apps ▸ Hammock. See [Using the iPhone app](/hammock/docs/iphone/).
