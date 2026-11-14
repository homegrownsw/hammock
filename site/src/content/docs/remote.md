---
title: Away from this Mac
description: Remote access, pairing your iPhone, linking your other computers, sharing a Group with other people, and the Hammock Key.
---

Your panes run on your Mac, and keep running whether or not you're looking at them. This page is about reaching them from somewhere else: your iPhone, your other computers, and other people. Everything here is off until you turn it on.

## Remote access

Remote access lets your paired devices and linked computers connect to this Mac directly, over an encrypted connection. Nothing goes through a server of ours. It listens only on the networks you choose:

- **Tailscale**, which works from anywhere your devices are on your tailnet.
- **A local network or a VPN**, chosen by its subnet: `192.168.1.0/24` for your home Wi-Fi, say, or your WireGuard VPN's subnet. Hammock listens there only while this Mac is on that network, and stops when it leaves.

Being on a chosen network isn't enough to get in: every device and computer still has to be paired or linked first, and signs in with its own key.

Turn it on in Hammock ▸ Settings ▸ Devices: switch on **Allow paired devices to connect remotely**, then turn on the networks you want under **Networks Hammock listens on**. Linked computers need it on, so while any are linked you can't turn it off.

**Extra addresses** are given out in pairing codes and links as well as this Mac's own: a DNS name (`mac.example.net`), a VPN endpoint, or a port forwarded from your router (`mac.example.net:7433`). Hammock's usual port is 7433.

From the command line:

```sh
hmk remote networks                      # what it can listen on (* chosen), and extra addresses
hmk remote enable tailscale
hmk remote enable 192.168.1.0/24
hmk remote address add mac.example.net
hmk remote on
```

The same settings are `remote-network` and `remote-address` in the [config file](/hammock/docs/reference/config/).

:::caution
Choose a home network by its subnet, and Hammock also listens on any other network with the same subnet, such as a café's Wi-Fi. Anyone there still needs a paired key to get in, but if you'd rather not take that chance, use Tailscale.
:::

## Pairing devices

Pairing is how your iPhone gets in. In Hammock ▸ Settings ▸ Devices, under **Pair a Device**, click **Show Pairing Code** (or choose File ▸ Pair a Device…), and scan the code with Hammock on your iPhone. A code works once and expires after 10 minutes. [Installing on iPhone](/hammock/docs/install/iphone/) walks through it.

On a Linux server, or in a terminal, `hmk pair --print` prints the code as a QR code right there. Plain `hmk pair` shows it in the Mac app.

Your paired devices are listed under **Paired Devices**, each with when it was last seen. **Revoke** one to cut it off. From the command line:

```sh
hmk devices                  # list paired devices
hmk devices revoke ID        # revoke one
```

Everything a paired device does is logged: see **Recent Activity** in Settings ▸ Devices, or `hmk audit`.

## Linked computers

Link your other Macs and Linux servers, and each one shows the others' Groups in its sidebar as well as its own, badged with the computer they're on. You can open tabs, start agents and type in panes on any of them from whichever one you're at. The row of computers at the foot of the sidebar shows one computer at a time.

To link one, choose File ▸ Link a Computer… (⌃⌘L) on either computer. It lists your computers running Hammock that it can find: those on your tailnet that belong to your own Tailscale account, and those on the local networks this Mac listens on. On a VPN, type the other computer's address instead.

Both computers then show a six-digit code. Check they're the same on both screens, and confirm on both (**They Match**). The code check is what stops anyone in between: if the codes differ, choose **They're Different**. Linking turns remote access on for both computers, and once linked, the new computer links with your other linked computers too.

Off Tailscale, a computer can only ask to link while you're linking on the other one as well (Link a Computer open there, or `hmk link --accept` run there in the last ten minutes). A request nobody started is refused without asking you.

From the command line, which is how you link a Linux server:

```sh
hmk link                     # computers you could link
hmk link Studio              # link one, by name or address
hmk link --accept            # on the other computer: answer it, with its code
hmk link pending             # requests waiting, with their codes
hmk link confirm 123456      # confirm one, only if the code matches the other screen
hmk computers                # your linked computers
```

`hmk link --invite` prints a link to this computer to paste on the other one.

Several commands take `--on COMPUTER` to work on a linked computer: `hmk ls`, `hmk group new`, `hmk agent`, `hmk sessions` and `hmk resume`. For example, to start Claude Code in a project on a server:

```sh
hmk agent claude --on Studio --dir ~/Developer/app
```

To unlink, use Settings ▸ Devices ▸ Your Computers, or `hmk computers unlink COMPUTER`. That unlinks it from all your computers, and each one refuses it from then on, including one that was off at the time, once it hears. If a computer is lost or stolen, unlink it from another one.

:::caution
A linked computer has full control of yours, and you of it: anything you can do in Hammock there, it can do here. Link only your own computers.
:::

### Your SSH keys on linked computers

Like `ssh -A`, panes on a linked computer use the SSH keys of the computer you're at. Run `git push` in a pane on your server while you're at your laptop, and it signs with your laptop's key. Only listing and signing go over the link; the keys stay where they are.

- An agent that asks to use a key from another computer asks you first.
- Every signature made for another computer goes in the audit log on both.
- Groups shared with other people never use your keys.

Turn it off in Settings ▸ SSH, with **Use the SSH keys of the computer you're at**, or `ssh-agent-forwarding = false` in the config file. It applies to new panes.

## Sharing a Group with other people

You can share a Group with someone else running Hammock. Everything in it keeps running on your Mac; they see it in their sidebar beside their own Groups. Each person has one of two roles:

- **View**: they watch the Group's panes, but can't type in them.
- **Drive**: they type in its panes and open tabs, as you can. Their agents can drive too.

:::danger
A Driver can run any command as you on your Mac. Give Drive only to people you'd hand your keyboard to.
:::

Sharing is off until you turn on **Allow Group sharing** in Hammock ▸ Settings ▸ Sharing. Then:

1. Choose **Share Group…**: in the File menu, the toolbar, or by right-clicking a Group and choosing **Share…**.
2. Click **Create Invite Link**, and send the link to the person.
3. They open it in Hammock (File ▸ Join Shared Group…, or `hmk join LINK`) and ask to join. You see their name and their key's fingerprint.
4. Let them in to view or to drive, or decline. You approve each person once per Group.

Requests to join also appear in Needs You on your Mac and on your iPhone, and as a notification on your iPhone, with **Let View** and **Decline**. **Let Drive…** opens the app first, where you can see their key.

People you share with reach your Mac over Tailscale only, so Tailscale must be one of the networks remote access listens on. If they're on another tailnet, share your Mac with them in Tailscale first.

In the share sheet you can change anyone between **Can view** and **Can drive**, **Remove** them, make a **New Link** (the old one stops working), **Turn Off Link**, or **Stop Sharing**. Removing someone, or stopping sharing, ends their connection at once.

From the command line:

```sh
hmk share app                # print the app Group's invite link
hmk share                    # who's in your shared Groups, and who's asking to join
hmk approve ID drive         # or view, or decline
hmk unshare app              # stop sharing it
hmk leave GROUP              # leave a Group someone shared with you
```

## Screen sharing

Your iPhone and your other Macs can also see and control this Mac's whole screen, for a macOS prompt an agent is stuck behind, or anything else only its windows can do. It's off until you turn it on in Settings ▸ Devices ▸ Screen Sharing. See [Screen sharing](/hammock/docs/screen-sharing/).

## The Hammock Key

The Hammock Key is an SSH key kept in your Mac's Secure Enclave. Its private half never leaves the Mac, and it signs only when you approve: with Touch ID on the Mac, or with Face ID on your iPhone. Use it for `ssh`, `git push` and signing commits, so that nothing running on your Mac, an agent included, can use it without you.

Create it in Hammock ▸ Settings ▸ SSH, with **Create Hammock Key…**, or:

```sh
hmk key create               # asks for Touch ID
hmk key pub                  # print its public key
hmk key copy-id me@mac.example.net   # add it to a server's authorized_keys
```

Settings ▸ SSH also has **Add to GitHub…** and **Copy to a Server…**. When something asks to sign, you can approve it once, or for a while in that pane: up to 10 minutes for your own programs, and 5 for an agent's.

To approve from your iPhone, let it approve under **Who Can Approve** (**Allow…**, then Touch ID), or with `hmk key allow DEVICE`. Requests then reach your iPhone as a notification: **Approve…** asks for Face ID.

`ssh` and `git` in Hammock's panes go through Hammock's own SSH agent, which offers your usual keys and the Hammock Key. If your `~/.ssh/config` sends ssh straight to another agent (a password manager's, say), ssh in Hammock's panes can't reach the Hammock Key. `hmk ssh setup` says whether that's the case, and `hmk ssh setup --apply` adds a block to `~/.ssh/config` that applies only in Hammock's panes. Settings ▸ SSH ▸ Your SSH Setup offers the same.

See [Permissions](/hammock/docs/permissions/#touch-id-and-the-hammock-key) for Touch ID.

## What keeps this safe

- **Pinned keys.** Every device and computer signs in with a key recorded when it was paired or linked, over TLS 1.3 pinned to this Mac's own certificate. Until it signs in, a connection can do almost nothing.
- **You, not your agents.** Pairing, linking, sharing, turning on remote access and answering permission requests are done directly only in the Hammock app. Asked from anywhere else, such as `hmk` in a terminal, they wait for you to allow them in the Mac app or on your iPhone. Agents are refused them outright. Nothing answers its own request.
- **Roles.** A Viewer can only watch. A Driver, or a linked computer, can run anything as you.
- **The audit log.** What paired devices, linked computers and agents do is logged: `hmk audit`.

Agents, and anything else running as you, could in the end do what you can. For a real boundary around an agent, use its own sandbox.

Found a security problem? Please report it privately: see [Reporting issues](/hammock/docs/reporting-issues/).
