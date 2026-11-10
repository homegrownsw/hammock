# Hammock on Linux (and Unraid)

The daemon (`hmkd`), the CLI and TUI (`hmk`) and the pane holders run on Linux: a VPS, a home server, an
Unraid box. Sessions live there; your Mac shows its Groups as a linked computer ([ADR-0013](adr/0013-linked-computers.md)), and your iPhone,
paired with it, hears when an agent there needs you while you're away from your Macs. There's no app on
Linux: everything is `hmk`, so a server you reach over SSH can be set up, linked and paired without a screen.

## Install

The tarballs come with each Mac release, on GitHub
(`https://homegrownsw.com/hammock/linux/hammock-linux-amd64.tar.gz` is the latest, and `arm64`). They need glibc 2.28 or later and nothing else (libghostty-vt is linked in).

```sh
tar -xzf hammock-linux-amd64.tar.gz && cd hammock-*/
mkdir -p ~/.local/bin && cp hmk hmkd pane-holder ~/.local/bin/
hmk service install     # a systemd user service, from boot (lingering), panes kept across restarts
hmk hooks install       # your agents (Claude Code, Codex and others found) report to Hammock
```

Without systemd: `hmk start`, or `hmkd --dir DIR` from your init system. State is in
`$XDG_STATE_HOME/hammock` (`~/.local/state/hammock`; `HMK_DIR` overrides it), settings in
`~/.config/hammock/config`.

`hmk status` says whether it's running (exit 3 if not), and how many Groups, panes and agents it has.

## Reach it

Remote access listens only on the networks you choose ([ADR-0016](adr/0016-remote-access-on-any-network.md)). Tailscale is the easy one: it works from
anywhere, and only your own Tailscale user's computers may ask to link.

```sh
hmk remote networks              # what this server is on; * chosen
hmk remote enable tailscale      # or a subnet: hmk remote enable 192.168.1.0/24
hmk remote on
```

On a VPS without Tailscale, choose its public subnet (`hmk remote networks` marks it public): the
certificate is pinned and every computer and device signs in with its key, but anyone who can reach the
port can knock. `hmk remote address add HOST[:PORT]` gives out a DNS name or a forwarded port.

## Link your Mac

```sh
hmk link --accept       # waits for your Mac, shows its code, asks whether it matches
```

Then on the Mac: File ▸ Link a Computer…, and choose the server (or paste the link `hmk link --invite`
prints). Both show a six-digit code: if they match, confirm on both. From a script, or a second SSH
session: `hmk link pending` lists computers asking, with their codes, and `hmk link confirm CODE` (typing
the code the Mac shows) or `hmk link refuse ID` answers. `hmk computers` lists linked ones.

## Pair your iPhone

```sh
hmk pair                # a QR code in the terminal: scan it in Hammock on the iPhone (Devices ▸ Add Computer)
```

`hmk devices` lists paired devices; `hmk devices revoke ID` removes one. A server has nobody sitting at it,
so it pushes to your phone only while none of your linked Macs is in use (and drops the push if the agent is
answered first).

## Unraid

The plugin (`unraid/`) installs from `https://homegrownsw.com/hammock/linux/hammock.plg` (Plugins ▸ Install
Plugin). Its page (Settings, under User Utilities: Hammock) then shows its status, Start/Stop/Restart,
the Hammock folder, where panes keep their home, whether to start with the array, remote access, Link a Computer (with the code check), Pair a Device (a QR code) and Open Hammock (the TUI in a window).
Every one of those is an `hmk` command underneath; `hmk` works in Unraid's terminal too.

- **Everything in one folder**, by default `/mnt/user/appdata/hammock` (resolved to the pool it's on):
  `state/`, and `home/`, the panes' `HOME`, so dotfiles, `~/.claude`, history and Claude Code itself
  (its installer puts it in `~/.local/bin`) survive reboots. Unraid's own `/root` doesn't. If you choose Unraid's `/root`
  as the panes' home instead, the agents' own files (`~/.claude`, `~/.claude.json`, `~/.codex`) are still kept in
  the folder's `agents/`, so their sign-ins outlive reboots.
- **Runs as root**, as everything on Unraid does. Panes run bash with Unraid's `/etc/profile` minus its
  `HOME=/root` and `cd $HOME`, and with `IS_SANDBOX=1`, so Claude Code's skip-permissions mode works. That makes every pane a root shell on the
  server: a paired phone, a linked computer or anyone you let drive a shared Group can do anything root can,
  array included. Pair, link and share accordingly.
- **With the array:** it starts when the array does (unless you turn that off), and when the array stops it stops and ends its panes
  (their open files would keep the array from unmounting). On the next start they come back: layout,
  folders, scrollback, shells, agents resumed. A watchdog (cron, every minute) starts it if it should be
  running and isn't; Stop on the page holds it stopped.
- **Updates** come with each Mac release (the plugin manager checks the `.plg`). Removing the plugin stops
  Hammock and ends its panes, and leaves the Hammock folder where it is.

## Build

```sh
just linux              # dist/linux: binaries and tarballs for amd64 and arm64 (zig cross-compiles)
just unraid             # dist/unraid: the Slackware package and hammock.plg
```

`scripts/build-linux.sh` builds libghostty-vt once per Ghostty commit and architecture into the shared
cache. To run the Go tests on Linux, mount the checkout and the arm64 build of libghostty-vt into a Go
container:

```sh
lib=~/Library/Caches/hammock/ghostty/$(grep -v '^#' third_party/ghostty/VERSION | head -1)/linux-arm64
docker run --rm -v "$PWD:/src" -v "$lib:/ghostty:ro" -v "$(go env GOMODCACHE):/go/pkg/mod:ro" -w /src \
  -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local golang:1.27-bookworm sh -c '
    apt-get -qq update && apt-get -qq install -y pkg-config >/dev/null
    cp -r /ghostty /tmp/ghostty && sed -i "s|^prefix=.*|prefix=/tmp/ghostty|" /tmp/ghostty/share/pkgconfig/*.pc
    PKG_CONFIG_PATH=/tmp/ghostty/share/pkgconfig go test -tags ghostty ./...'
```
