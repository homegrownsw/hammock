---
title: Install on a Mac
description: Download Hammock, set it up, keep it running and remove it again.
---

Hammock for Mac needs **macOS 26 or later**. It's free and open source.

## Download and install

1. [Download Hammock](https://homegrownsw.com/hammock/download).
2. Open the disk image and drag Hammock to Applications.
3. Open Hammock from Applications.

Run it from Applications: from anywhere else it can't start at login or install `hmk`, and setup offers **Move to
Applications** to fix that.

## Setup

The first time it opens, Hammock walks you through a short setup. Every step can be skipped (**Set up later**), and
you can run it again from Help ▸ Welcome to Hammock….

- **Make it yours:** light, dark or automatic, a theme, and whether tabs sit in the sidebar under their Group or along the top.
- **Keep Hammock running:** two switches, both on by default.
  - **Run in the background at login** registers Hammock's daemon as a login item, so your terminals and agents
    keep going when the window is closed and come back after a restart. It runs as you, with no admin password.
    macOS may ask you to approve it: turn on Hammock in System Settings ▸ General ▸ Login Items & Extensions, and
    setup notices.
  - **Install the `hmk` command** (see [below](#the-hmk-command)).
- **Full Disk Access** (see [below](#full-disk-access)).
- **Connect your agents:** Hammock lists the coding agents it finds (Claude Code, Codex and others) and offers to
  install its hooks and MCP tools for them. **Show changes** shows what it will add; a backup of each file it
  changes is saved next to it, and your own hooks and servers are left alone. See [Agent setup](/hammock/docs/agents/setup/).
- **Finish:** open a project (a Group named after a folder, with Claude Code started there if you have it), open a
  shell, or look around.

## Keeping the daemon running

Hammock's daemon owns your panes. The app starts it if it isn't running, and quitting the app leaves it running, so
panes, agents and paired devices carry on.

To have it start when you log in, whether or not the app is open, turn on **Start the server at login** in
Settings ▸ General. To stop it, choose **Quit and Stop Server…** in Settings ▸ About (Hammock ▸ About Hammock): your
panes keep running and come back when you next open Hammock, unless you tick **Also end what's running in your
panes**.

## Full Disk Access

Like other terminals, Hammock works best with Full Disk Access. macOS records privacy decisions about the programs
in your panes against Hammock, and refuses some of them without ever asking. Without Full Disk Access, programs in
your panes can quietly fail to read your files or other apps' data: a password manager's CLI that can't reach its
desktop app, for example.

Setup offers it: open **Full Disk Access** in System Settings, drag Hammock's icon into the list, and choose **Quit &
Reopen** when macOS asks. Setup carries on where it left off. Settings ▸ General ▸ Privacy shows whether it's on,
with a button to open System Settings.

:::caution
Everything you run in Hammock gets Full Disk Access too, agents included. It's suggested, never required.
:::

## Updates

Hammock updates itself. It checks once a day and asks before installing. Check now with Hammock ▸ Check for
Updates…, or turn daily checks off in Settings ▸ About ▸ **Check for updates daily**. After an update the new daemon
takes over and your panes keep running.

## The hmk command

`hmk` is Hammock's command-line tool: it drives the workspace from any shell or script, and runs the TUI
(`hmk tui`). It's inside the app; setup links it onto your PATH, or use **Install** under Command-line tool in
Settings ▸ General. Hammock links it into the first of these folders it can write to:

- `/opt/homebrew/bin`
- `/usr/local/bin`
- `~/.local/bin` (created if need be)

If that folder isn't on your PATH yet, setup tells you which line to add to your shell's startup file. Check it
worked:

```sh
hmk status
```

See [The CLI](/hammock/docs/cli/) for what it can do.

## Where things live

| What | Where |
|---|---|
| Settings | `~/.config/hammock/config` ([Configuration](/hammock/docs/configuration/)) |
| State: Groups, panes, scrollback, keys | `~/Library/Application Support/Hammock` |
| The daemon's log | `~/Library/Application Support/Hammock/logs/hmkd.log` |

Settings ▸ General ▸ Server shows the state folder, and opens it in the Finder. To run `hmk` against a daemon with
a different state folder, set `HMK_DIR`:

```sh
HMK_DIR=/tmp/hammock-scratch hmk start
```

## Uninstall

Open Settings ▸ About (Hammock ▸ About Hammock) and choose **Uninstall…**. It stops the daemon and everything running
in your panes, removes Hammock's login item, the `hmk` command and the hooks and tools it added to your agents, and
moves Hammock to the Bin. Tick **Also move your settings and data to the Bin** to remove your config and state as well.

Your Hammock Key stays: delete it in Settings ▸ SSH first if you want it gone.
