---
title: Troubleshooting
description: Answers to common problems with Hammock on the Mac, the iPhone and the command line.
---

## Programs in my panes can't read files, or another app's data

Give Hammock Full Disk Access: Settings ▸ General ▸ Privacy ▸ **Open System Settings…**, add Hammock to the list and
choose **Quit & Reopen**. Everything you run in Hammock gets it too, agents included. See
[Install on a Mac](/hammock/docs/install/mac/#full-disk-access).

## An agent's state is wrong, or its session doesn't resume

Install Hammock's hooks: Settings ▸ Agents ▸ **Install Hooks & Tools**, or `hmk hooks install`. Without them,
Hammock guesses the state from the agent's screen and can't resume its session. See
[Setting up agents](/hammock/docs/agents/setup/).

## My iPhone can't connect to my Mac

Check each of these:

- On the Mac, Settings ▸ Devices: **Allow paired devices to connect remotely** is on.
- Under Remote Access, the network the iPhone is on is chosen. With none chosen, Hammock listens on Tailscale only.
- If you use Tailscale, it's connected on both the Mac and the iPhone.
- If you use your local network, the iPhone is on that Wi-Fi, not mobile data.
- The Mac is awake and the daemon is running (below).

`hmk remote networks` lists the Mac's networks, with the chosen ones marked `*`. See
[Install on iPhone](/hammock/docs/install/iphone/#networks).

## I don't get notifications on my iPhone

Allow notifications for Hammock in the iPhone's Settings, and open the app once while it's connected to your Mac.
The Mac only sends them while you're away from it: when its screen is locked, or you haven't touched it for a couple
of minutes.

## The daemon isn't running

```sh
hmk start
```

Opening the Hammock app also starts it. If it won't start, read its log at
`~/Library/Application Support/Hammock/logs/hmkd.log` (`~/.local/state/hammock/logs/hmkd.log` on Linux).

## My panes keep running after I quit the app

That's by design. To stop everything, choose **Quit and Stop Server…** in Settings ▸ About and tick **Also end
what's running in your panes**, or run `hmk stop --panes`.

## What comes back after a reboot?

Every Group, tab and pane, with its folder and scrollback. Shells start again, agents resume their sessions (with
hooks installed) and browser panes reopen their page. It happens at login with **Start the server at login** on in
Settings ▸ General, or else when you next open Hammock.

## hmk talks to the wrong daemon

Check you haven't set `HMK_DIR` and forgotten it (`echo $HMK_DIR`): outside a pane, `hmk` talks to the daemon in that
state folder. Inside a pane, it always talks to the daemon running that pane.

If `hmk` isn't found at all, install it from Settings ▸ General ▸ Command-line tool, and check its folder is on your
PATH ([Install on a Mac](/hammock/docs/install/mac/#the-hmk-command)).

## Resetting settings

Delete a setting's line from `~/.config/hammock/config` to reset it, or move the file away to reset them all:

```sh
mv ~/.config/hammock/config ~/.config/hammock/config.old
```

See [Configuration](/hammock/docs/configuration/).

## Still stuck?

See [Reporting issues](/hammock/docs/reporting-issues/) for how to tell us, and what to include.
