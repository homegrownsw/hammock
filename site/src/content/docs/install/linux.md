---
title: Install on Linux
description: Run Hammock's daemon, hmk and the TUI on a Linux server or Unraid, and link it to your Mac.
---

Hammock's daemon (`hmkd`) and its CLI and TUI (`hmk`) run on Linux: a VPS, a home server, an Unraid box. Your Mac
shows its Groups as a [linked computer](/hammock/docs/remote/), and your iPhone can pair with it.

There's no app on Linux: everything is `hmk`, so it can be set up over SSH. Use `hmk tui` for the
[full-screen workspace](/hammock/docs/tui/).

## Install

Tarballs for amd64 and arm64 come with each Mac release. They need glibc 2.28 or later.

- [hammock-linux-amd64.tar.gz](https://homegrownsw.com/hammock/linux/hammock-linux-amd64.tar.gz)
- [hammock-linux-arm64.tar.gz](https://homegrownsw.com/hammock/linux/hammock-linux-arm64.tar.gz)

```sh
tar -xzf hammock-linux-amd64.tar.gz && cd hammock-*/
mkdir -p ~/.local/bin && cp hmk hmkd pane-holder ~/.local/bin/
hmk service install     # a systemd user service, from boot, panes kept across restarts
hmk hooks install       # your agents (Claude Code, Codex and others found) report to Hammock
```

`hmk service status` shows how the service is doing, and `hmk service uninstall` removes it. Without systemd, run
`hmk start`, or `hmkd --dir DIR` from your init system. `hmk status` says whether the daemon is running.

| What | Where |
|---|---|
| State | `~/.local/state/hammock` |
| Settings | `~/.config/hammock/config` ([Configuration](/hammock/docs/configuration/)) |

## Reach it

Remote access listens only on the networks you choose. Tailscale is the easy one: it works from anywhere, and only
your own Tailscale user's computers may ask to link.

```sh
hmk remote networks              # what this server is on; * chosen
hmk remote enable tailscale      # or a subnet: hmk remote enable 192.168.1.0/24
hmk remote on
```

On a VPS without Tailscale, choose its public subnet. Every computer and device still signs in with its key, but
anyone who can reach the port can knock. `hmk remote address add HOST[:PORT]` gives out a DNS name or a forwarded
port as well.

## Link your Mac

On the server:

```sh
hmk link --accept       # waits for your Mac, shows its code, asks whether it matches
```

Then on the Mac, choose File ▸ Link a Computer… and pick the server (or paste the link `hmk link --invite` prints).
Both show a six-digit code: if they match, confirm on both. `hmk computers` lists linked computers.

## Pair your iPhone

```sh
hmk pair                # a QR code in the terminal
```

Scan it in Hammock on the iPhone (Devices ▸ Add Computer). `hmk devices revoke ID` removes a paired device. A server
notifies your iPhone only while none of your linked Macs is in use.

## Unraid

Install the plugin from `https://homegrownsw.com/hammock/linux/hammock.plg` (Plugins ▸ Install Plugin). Its page,
under Settings ▸ User Utilities ▸ Hammock, starts and stops Hammock, sets its folder, turns on remote access, links
a computer, pairs a device and opens the TUI. `hmk` works in Unraid's terminal too.

- **Everything in one folder**, by default `/mnt/user/appdata/hammock`, including `home/`, the panes' `HOME`. Your
  dotfiles, shell history, and Claude Code and its sign-in survive reboots, which Unraid's own `/root` doesn't.
- **It runs as root**, as everything on Unraid does, with `IS_SANDBOX=1` so Claude Code's skip-permissions mode
  works.
- **With the array:** Hammock starts when the array does, unless you turn that off. When the array stops, Hammock
  ends its panes, and brings them back on the next start, agents resumed.
- **Updates** come with each Mac release, through Unraid's plugin manager. Removing the plugin leaves the Hammock
  folder where it is.

:::caution
On Unraid every pane is a root shell. A paired phone, a linked computer or anyone you let drive a shared Group can do
anything root can, the array included. Pair, link and share accordingly.
:::
