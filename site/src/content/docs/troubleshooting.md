---
title: Troubleshooting
description: Answers to common problems with Hammock on the Mac, your phone and the command line.
---

## Programs in my panes can't read files, or another app's data

Give Hammock Full Disk Access: Settings ▸ General ▸ Privacy ▸ **Open System Settings…**, add Hammock to the list and
choose **Quit & Reopen**. Everything you run in Hammock gets it too, agents included. See
[Install on a Mac](/hammock/docs/install/mac/#full-disk-access).

## An agent's state is wrong, or its session doesn't resume

Install Hammock's hooks: Settings ▸ Agents ▸ **Install Hooks & Tools**, or `hmk hooks install`. Without them,
Hammock guesses the state from the agent's screen and can't resume its session. See
[Setting up agents](/hammock/docs/agents/setup/).

## My phone can't connect to my Mac

Check each of these:

- On the Mac, Settings ▸ Devices: **Allow paired devices to connect remotely** is on.
- Under Remote Access, the network the phone is on is chosen. With none chosen, Hammock listens on Tailscale only.
- If you use Tailscale, it's connected on both the Mac and the phone.
- If you use your local network, the phone is on that Wi-Fi, not mobile data.
- The Mac is awake and the Hammock Server is running (below).

`hmk remote networks` lists the Mac's networks, with the chosen ones marked `*`. See
[Remote access](/hammock/docs/remote/#remote-access).

If all of that's right and it still can't connect, or a linked computer shows as asleep while it's on, run
`hmk remote restart` on the computer that can't be reached: it listens again, with the same settings.

## I don't get notifications on my phone

Allow notifications for Hammock in the phone's Settings (on Android, Settings ▸ Apps ▸ Hammock ▸ Notifications), and
open the app once while it's connected to your Mac.
The Mac only sends them while you're away from it: when its screen is locked, or you haven't touched it for a couple
of minutes.

## The Hammock Server isn't running

```sh
hmk start
```

Opening the Hammock app also starts it. If it won't start, read its log at
`~/Library/Application Support/Hammock/logs/hmkd.log` (`~/.local/state/hammock/logs/hmkd.log` on Linux).

## Remote access says this computer's identity or certificate is damaged

Hammock won't replace `identity.key`, or `tls/cert.pem` and `tls/key.pem`, in its state folder when it can't read
them, since your phones, linked computers and shared Groups know this computer by them. Until they're fixed, your
panes and Groups on this computer work, but remote access is off, and with a damaged identity so are linked
computers and shared Groups. Restore the files from a backup, or fix their permissions, then restart the Hammock
Server (`hmk stop`, then `hmk start`). If they're lost, delete them (both `tls` files together) and restart it: it
makes new ones, and you pair your phones and link your computers again.

## My panes keep running after I quit the app

That's by design. To stop everything, choose **Quit and Stop Server…** in Settings ▸ About and tick **Also end
what's running in your panes**, or run `hmk stop --panes`.

## What comes back after a reboot?

Every Group, tab and pane, with its folder and scrollback. Shells start again, agents resume their sessions (with
hooks installed) and browser panes reopen their page. It happens at login with **Start the server at login** on in
Settings ▸ General, or else when you next open Hammock.

## hmk talks to the wrong server

Check you haven't set `HMK_DIR` and forgotten it (`echo $HMK_DIR`): outside a pane, `hmk` talks to the Hammock Server in that
state folder. Inside a pane, it always talks to the server running that pane.

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
