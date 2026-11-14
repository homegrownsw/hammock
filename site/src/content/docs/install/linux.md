---
title: Install on Linux
description: Run Hammock's daemon, hmk and the TUI on a Linux server or Unraid, and link it to your Mac.
---

Hammock's daemon (`hmkd`), its CLI and TUI (`hmk`) and the pane holders run on Linux: a VPS, a home server, an
Unraid box. Your sessions live there. Your Mac shows its Groups as a [linked computer](/hammock/docs/remote/), and
your iPhone, paired with it, hears when an agent there needs you while you're away from your Macs.

There's no app on Linux: everything is `hmk`, so a server you reach over SSH can be set up, linked and paired
without a screen. Use `hmk tui` for the [full-screen workspace](/hammock/docs/tui/).

## Install

Tarballs for amd64 and arm64 come with each Mac release. They need glibc 2.28 or later and nothing else.

- [hammock-linux-amd64.tar.gz](https://homegrownsw.com/hammock/linux/hammock-linux-amd64.tar.gz)
- [hammock-linux-arm64.tar.gz](https://homegrownsw.com/hammock/linux/hammock-linux-arm64.tar.gz)

```sh
tar -xzf hammock-linux-amd64.tar.gz && cd hammock-*/
mkdir -p ~/.local/bin && cp hmk hmkd pane-holder ~/.local/bin/
hmk service install     # a systemd user service, from boot, panes kept across restarts
hmk hooks install       # your agents (Claude Code, Codex and others found) report to Hammock
```

`hmk service install` also turns on lingering, so the daemon starts at boot and keeps running when you log out. If
it can't, it prints the `loginctl enable-linger` command to run. `hmk service status` shows how it's doing, and
`hmk service uninstall` removes it.

Without systemd, run `hmk start`, or `hmkd --dir DIR` from your init system.

| What | Where |
|---|---|
| State | `$XDG_STATE_HOME/hammock` (`~/.local/state/hammock`); `HMK_DIR` overrides it |
| Settings | `~/.config/hammock/config` ([Configuration](/hammock/docs/configuration/)) |

`hmk status` says whether the daemon is running (it exits with 3 if not), and how many Groups, panes and agents it
has.

## Reach it

Remote access listens only on the networks you choose. Tailscale is the easy one: it works from anywhere, and only
your own Tailscale user's computers may ask to link.

```sh
hmk remote networks              # what this server is on; * chosen
hmk remote enable tailscale      # or a subnet: hmk remote enable 192.168.1.0/24
hmk remote on
```

On a VPS without Tailscale, choose its public subnet (`hmk remote networks` marks it public). The certificate is
pinned and every computer and device signs in with its key, but anyone who can reach the port can knock.
`hmk remote address add HOST[:PORT]` gives out a DNS name or a forwarded port as well.

## Link your Mac

On the server:

```sh
hmk link --accept       # waits for your Mac, shows its code, asks whether it matches
```

Then on the Mac, choose File ▸ Link a Computer… and pick the server (or paste the link `hmk link --invite` prints).
Both show a six-digit code: if they match, confirm on both.

From a script, or a second SSH session, `hmk link pending` lists computers asking to link, with their codes.
`hmk link confirm CODE` (typing the code the Mac shows) or `hmk link refuse ID` answers. `hmk computers` lists linked
ones.

## Pair your iPhone

```sh
hmk pair                # a QR code in the terminal
```

Scan it in Hammock on the iPhone (Devices ▸ Add Computer). `hmk devices` lists paired devices, and
`hmk devices revoke ID` removes one. A server has nobody sitting at it, so it notifies your iPhone only while none
of your linked Macs is in use, and drops the notification if the agent is answered first.

## Unraid

Install the plugin from `https://homegrownsw.com/hammock/linux/hammock.plg` (Plugins ▸ Install Plugin). Its page,
under Settings ▸ User Utilities ▸ Hammock, shows its status and has Start, Stop and Restart, the Hammock folder,
where panes keep their home, whether to start with the array, remote access, Link a Computer (with the code check),
Pair a Device (a QR code) and Open Hammock (the TUI in a window). Each is an `hmk` command underneath, and `hmk`
works in Unraid's terminal too.

- **Everything in one folder**, by default `/mnt/user/appdata/hammock`: `state/`, and `home/`, the panes' `HOME`.
  Your dotfiles, `~/.claude`, shell history and Claude Code itself (its installer puts it in `~/.local/bin`) survive
  reboots, which Unraid's own `/root` doesn't. If you choose `/root` as the panes' home instead, the agents' own
  files (`~/.claude`, `~/.claude.json`, `~/.codex`) are still kept in the folder's `agents/`, so their sign-ins
  outlive reboots.
- **It runs as root**, as everything on Unraid does. Panes run bash with Unraid's `/etc/profile` (without its
  `HOME=/root` and `cd $HOME`), and with `IS_SANDBOX=1`, so Claude Code's skip-permissions mode works. That makes
  every pane a root shell on the server (see the caution below).
- **With the array:** Hammock starts when the array does (unless you turn that off). When the array stops, it stops
  and ends its panes, whose open files would keep the array from unmounting. On the next start they come back:
  layout, folders, scrollback, shells, and agents resumed. A watchdog starts it every minute if it should be running
  and isn't; Stop on the page keeps it stopped.
- **Updates** come with each Mac release, through Unraid's plugin manager. Removing the plugin stops Hammock and
  ends its panes, and leaves the Hammock folder where it is.

:::caution
On Unraid every pane is a root shell. A paired phone, a linked computer or anyone you let drive a shared Group can do
anything root can, the array included. Pair, link and share accordingly.
:::
