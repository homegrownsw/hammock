---
title: Troubleshooting
description: Answers to common problems with Hammock on the Mac, the iPhone and the command line.
---

## Programs in my panes can't read files, or another app's data

Give Hammock Full Disk Access. macOS records privacy decisions about the programs in your panes against Hammock, and
refuses some of them without asking: a program fails quietly, with no dialog. A password manager's CLI that can't
reach its desktop app is a typical case.

Settings ▸ General ▸ Privacy shows whether Full Disk Access is on, and **Open System Settings…** takes you there.
Add Hammock to the list and choose **Quit & Reopen** when macOS asks. Everything you run in Hammock gets it too,
agents included. See [Install on a Mac](/hammock/docs/install/mac/#full-disk-access).

## An agent's state is wrong, or its session doesn't resume

Without hooks, Hammock works out what an agent is doing from its process and what's on its screen, which can lag or
miss a state. Hooks make it exact, and tell Hammock which session the pane is running, so it can resume it after a
restart. Install them from Settings ▸ Agents ▸ **Install Hooks & Tools**, or:

```sh
hmk hooks install --dry-run   # see what would change
hmk hooks install
```

It backs up each file it changes and leaves your own hooks alone. See [Agent setup](/hammock/docs/agents/setup/).

## My iPhone can't connect to my Mac

The iPhone reaches your Mac directly over a network you've chosen, so check each of these:

- On the Mac, Settings ▸ Devices: **Allow paired devices to connect remotely** is on.
- Under Remote Access, the network the iPhone is on is chosen. With none chosen, Hammock listens on Tailscale only.
- If you use Tailscale, it's connected on both the Mac and the iPhone.
- If you use your local network, the iPhone is on that Wi-Fi, not mobile data.
- The Mac is awake and the daemon is running (below).

`hmk remote networks` lists the networks the Mac is on and marks the chosen ones with `*`. See
[Install on iPhone](/hammock/docs/install/iphone/#networks).

## I don't get notifications on my iPhone

Allow notifications for Hammock in the iPhone's Settings, and open the app once while it's connected to your Mac, so
the Mac learns where to send them. The app's Settings tab says whether push is on, under **Notify Me When**.

The Mac only sends them while you're away from it: its screen is locked, or you haven't touched it for a couple of
minutes. While you're at it, the Mac notifies you itself.

## The daemon isn't running

```sh
hmk status     # exits with 3 when the daemon isn't running
hmk start
```

Opening the Hammock app also starts it. To have it start at login, turn on **Start the server at login** in
Settings ▸ General. If it won't start, its log is at `~/Library/Application Support/Hammock/logs/hmkd.log`
(`~/.local/state/hammock/logs/hmkd.log` on Linux; under `hmk service install`, `journalctl --user -u hammock`).

## My panes keep running after I quit the app

That's by design: the daemon owns your panes, so quitting the app, updating it or a crash doesn't stop them. To
stop everything, choose **Quit and Stop Server…** in Settings ▸ About and tick **Also end what's running in your
panes**, or:

```sh
hmk stop --panes
```

`hmk stop` on its own stops the daemon and leaves your panes running; they come back when it starts again.

## What comes back after a reboot?

Every Group, tab and pane, with its folder and the scrollback it had. Shells start again, and agents resume their
sessions (with hooks installed: above). Browser panes come back at their page. For this to happen at login, turn on
**Start the server at login** in Settings ▸ General; otherwise it happens when you next open Hammock.

## hmk talks to the wrong daemon

Outside a pane, `hmk` uses the daemon whose state folder is `HMK_DIR`, or by default
`~/Library/Application Support/Hammock` (`~/.local/state/hammock` on Linux). Check you haven't set `HMK_DIR`
somewhere and forgotten it:

```sh
echo $HMK_DIR
hmk status
```

Inside a Hammock pane, `hmk` always talks to the daemon that runs that pane, whatever `HMK_DIR` says.

If `hmk` isn't found at all, install it from Settings ▸ General ▸ Command-line tool, and check its folder is on your
PATH ([Install on a Mac](/hammock/docs/install/mac/#the-hmk-command)).

## Resetting settings

Your settings are all in `~/.config/hammock/config`, and a key you leave out takes its default. To reset one, delete
its line. To reset them all, move the file away:

```sh
mv ~/.config/hammock/config ~/.config/hammock/config.old
```

Hammock picks the change up within a couple of seconds. See [Configuration](/hammock/docs/configuration/).

## Still stuck?

See [Reporting issues](/hammock/docs/reporting-issues/) for how to tell us, and what to include.
