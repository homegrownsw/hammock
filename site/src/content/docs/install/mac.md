---
title: Install on a Mac
description: Download Hammock, set it up, keep it running and remove it again.
---

Hammock for Mac needs **macOS 26 or later**. It's free and open source.

## Download and install

1. [Download Hammock](https://homegrownsw.com/hammock/download).
2. Open the disk image and drag Hammock to Applications.
3. Open Hammock from Applications.

## Setup

The first time it opens, Hammock walks you through a short setup. Any step can be skipped, and Help ▸ Welcome to
Hammock… runs it again.

- **Make it yours:** light, dark or automatic, a theme, and whether tabs sit in the sidebar under their Group or along the top.
- **Keep Hammock running:** two switches, both on by default.
  - **Run in the background at login**, so your terminals and agents keep going when the window is closed and come
    back after a restart. If macOS asks, turn Hammock on in System Settings ▸ General ▸ Login Items & Extensions.
  - **Install the `hmk` command** (see [below](#the-hmk-command)).
- **Full Disk Access** (see [below](#full-disk-access)).
- **Connect your agents:** installs Hammock's hooks and MCP tools for the coding agents it finds. See
  [Setting up agents](/hammock/docs/agents/setup/).

## Keeping the daemon running

Hammock's daemon owns your panes, so quitting the app leaves them running. **Start the server at login** in
Settings ▸ General starts it when you log in, app or no app. To stop it, choose **Quit and Stop Server…** in
Settings ▸ About.

## Full Disk Access

Like other terminals, Hammock works best with Full Disk Access: without it, macOS can quietly stop programs in your
panes reading your files or other apps' data. Setup offers it, and Settings ▸ General ▸ Privacy shows whether it's
on. See [Permissions](/hammock/docs/permissions/).

:::caution
Everything you run in Hammock gets Full Disk Access too, agents included. It's suggested, never required.
:::

## Updates

Hammock checks for updates daily and asks before installing one (Hammock ▸ Check for Updates… to check now). Your
panes keep running through an update.

## The hmk command

`hmk` is Hammock's command line ([The CLI](/hammock/docs/cli/)). Setup puts it on your PATH, or use **Install**
under Command-line tool in Settings ▸ General. It goes in the first of `/opt/homebrew/bin`, `/usr/local/bin` or
`~/.local/bin` that Hammock can write to.

## Where things live

| What | Where |
|---|---|
| Settings | `~/.config/hammock/config` ([Configuration](/hammock/docs/configuration/)) |
| State: Groups, panes, scrollback, keys | `~/Library/Application Support/Hammock` |
| The daemon's log | `~/Library/Application Support/Hammock/logs/hmkd.log` |


## Uninstall

Choose **Uninstall…** in Settings ▸ About. It stops everything Hammock runs, removes the `hmk` command and the hooks
it added to your agents, and moves Hammock to the Bin. Tick **Also move your settings and data to the Bin** to remove
those too. Your Hammock Key stays: delete it in Settings ▸ SSH first if you want it gone.
