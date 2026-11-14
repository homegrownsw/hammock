---
title: Away from this Mac
description: Remote access, pairing your iPhone, linking your other computers, sharing a Group with other people, and the Hammock Key.
---

This page is about reaching your panes from your iPhone, your other computers, and other people. Everything here is off until you turn it on.

## Remote access

Remote access lets your paired devices and linked computers connect to this Mac directly, encrypted, with no server of ours in between. It listens only on the networks you choose:

- **Tailscale**, from anywhere on your tailnet.
- **A local network or a VPN**, by its subnet (`192.168.1.0/24` for home Wi-Fi, say), only while this Mac is on it.

Every device and computer still has to be paired or linked first, and signs in with its own key.

Turn it on in Settings ▸ Devices: switch on **Allow paired devices to connect remotely**, then choose the networks under **Networks Hammock listens on**. It can't be turned off while any computers are linked.

**Extra addresses**, such as a DNS name or a port forwarded from your router (`mac.example.net:7433`), go into pairing codes and links too. Hammock's usual port is 7433.

From the command line (or `remote-network` and `remote-address` in the [config file](/hammock/docs/reference/config/)):

```sh
hmk remote networks                      # what it can listen on (* chosen), and extra addresses
hmk remote enable tailscale
hmk remote enable 192.168.1.0/24
hmk remote address add mac.example.net
hmk remote on
```

:::caution
A network chosen by its subnet also matches any other network with the same subnet, such as a café's Wi-Fi. Anyone there still needs a paired key, but if you'd rather not take that chance, use Tailscale.
:::

## Pairing devices

In Settings ▸ Devices, click **Show Pairing Code** (or File ▸ Pair a Device…) and scan it with Hammock on your iPhone. A code works once, for 10 minutes. See [Installing on iPhone](/hammock/docs/install/iphone/). On a Linux server, `hmk pair --print` prints the code in the terminal.

**Revoke** a device under **Paired Devices** to cut it off, or with `hmk devices revoke ID`. What paired devices do is logged in **Recent Activity**, or `hmk audit`.

## Linked computers

Link your other Macs and Linux servers, and each one shows the others' Groups in its sidebar, badged with the computer they're on. You can open tabs, start agents and type in panes on any of them from whichever one you're at.

To link one, choose File ▸ Link a Computer… (⌃⌘L). It lists the computers running Hammock it can find on your own tailnet and on the networks this Mac listens on; on a VPN, type the address. Both computers then show a six-digit code. Choose **They Match** on both only if they're the same, or **They're Different** if not. Linking turns remote access on for both, and the new computer links with your other linked computers too.

Off Tailscale, the other computer must be linking too (Link a Computer open, or `hmk link --accept`).

From the command line, which is how you link a Linux server:

```sh
hmk link                     # computers you could link
hmk link Studio              # link one, by name or address
hmk link --accept            # on the other computer: answer it, with its code
hmk link pending             # requests waiting, with their codes
hmk link confirm 123456      # confirm one, only if the code matches the other screen
hmk link --invite            # a link to this computer, to paste on the other
hmk computers                # your linked computers
```

`hmk ls`, `hmk group new`, `hmk agent`, `hmk sessions` and `hmk resume` take `--on COMPUTER`:

```sh
hmk agent claude --on Studio --dir ~/Developer/app
```

To unlink, use Settings ▸ Devices ▸ Your Computers, or `hmk computers unlink COMPUTER`. That unlinks it from all your computers, so if one is lost or stolen, unlink it from another.

:::caution
A linked computer has full control of yours, and you of it: anything you can do in Hammock there, it can do here. Link only your own computers.
:::

### Your SSH keys on linked computers

Like `ssh -A`, panes on a linked computer use the SSH keys of the computer you're at: `git push` on your server signs with your laptop's key, which never leaves the laptop. An agent asks you before using a key from another computer, each signature is logged on both, and shared Groups never use your keys.

Turn it off in Settings ▸ SSH (**Use the SSH keys of the computer you're at**), or with `ssh-agent-forwarding = false` in the config file, for new panes.

## Sharing a Group with other people

You can share a Group with someone else running Hammock. It keeps running on your Mac, and shows in their sidebar. Each person has one of two roles:

- **View**: they watch the Group's panes, but can't type in them.
- **Drive**: they type in its panes and open tabs, as you can. Their agents can drive too.

:::danger
A Driver can run any command as you on your Mac. Give Drive only to people you'd hand your keyboard to.
:::

Turn on **Allow Group sharing** in Settings ▸ Sharing. Then:

1. Choose **Share Group…** from the File menu or the toolbar, or right-click a Group and choose **Share…**.
2. Click **Create Invite Link**, and send the link to the person.
3. They open it in Hammock (File ▸ Join Shared Group…, or `hmk join LINK`) and ask to join. You see their name and their key's fingerprint.
4. Let them in to view or to drive, or decline. You approve each person once per Group.

Requests also appear in Needs You on your Mac and iPhone. **Let Drive…** opens the app first, so you can see their key.

People you share with reach your Mac over Tailscale only, so remote access must listen on Tailscale. If they're on another tailnet, share your Mac with them in Tailscale first.

The share sheet changes anyone between **Can view** and **Can drive**, **Remove**s them, makes a **New Link** (the old one stops working), **Turn Off Link** or **Stop Sharing**. Removing someone ends their connection at once.

From the command line:

```sh
hmk share app                # print the app Group's invite link
hmk share                    # who's in your shared Groups, and who's asking to join
hmk approve ID drive         # or view, or decline
hmk unshare app              # stop sharing it
hmk leave GROUP              # leave a Group someone shared with you
```

## Screen sharing

Your iPhone and your other Macs can also see and control this Mac's whole screen, say for a macOS prompt an agent is stuck behind. Turn it on in Settings ▸ Devices ▸ Screen Sharing. See [Screen sharing](/hammock/docs/screen-sharing/).

## The Hammock Key

The Hammock Key is an SSH key kept in your Mac's Secure Enclave. It never leaves the Mac, and signs only when you approve, with Touch ID on the Mac or Face ID on your iPhone. Use it for `ssh`, `git push` and signing commits, so nothing on your Mac, an agent included, can use it without you.

Create it in Hammock ▸ Settings ▸ SSH, with **Create Hammock Key…**, or:

```sh
hmk key create               # asks for Touch ID
hmk key pub                  # print its public key
hmk key copy-id me@mac.example.net   # add it to a server's authorized_keys
```

Settings ▸ SSH also has **Add to GitHub…** and **Copy to a Server…**. You can approve a signature once, or for a while in that pane (up to 10 minutes, or 5 for an agent).

To approve from your iPhone, allow it under **Who Can Approve**, or with `hmk key allow DEVICE`. Requests then reach it as a notification.

`ssh` and `git` in Hammock's panes go through Hammock's own SSH agent, which offers your usual keys and the Hammock Key. If your `~/.ssh/config` sends ssh to another agent (a password manager's, say), run `hmk ssh setup` to check, and `hmk ssh setup --apply` to fix it for Hammock's panes only. Settings ▸ SSH ▸ Your SSH Setup does the same.

See [Permissions](/hammock/docs/permissions/#touch-id-and-the-hammock-key) for Touch ID.

## What keeps this safe

- **Pinned keys.** Every device and computer signs in with the key recorded when it was paired or linked, over TLS pinned to this Mac's certificate.
- **You, not your agents.** Pairing, linking, sharing, turning on remote access and answering permission requests happen directly only in the Hammock app. Asked from anywhere else, such as `hmk`, they wait for you to allow them on your Mac or iPhone. Agents are refused them outright.
- **The audit log.** What paired devices, linked computers and agents do is logged: `hmk audit`.

Agents, and anything else running as you, could in the end do what you can. For a real boundary around an agent, use its own sandbox.

Found a security problem? Please report it privately: see [Reporting issues](/hammock/docs/reporting-issues/).
