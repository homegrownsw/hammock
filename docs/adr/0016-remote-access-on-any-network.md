# ADR-0016: Remote access on any network

Status: Accepted · 2026-11-08 · Amends [ADR-0007](0007-tailscale-and-device-pairing.md) (Tailscale only) and [ADR-0013](0013-linked-computers.md) (linking only your own Tailscale user's computers)

(First numbered ADR-0015, a number it shared with [ADR-0015](0015-agent-registry.md); renumbered 2026-11-10.)

## Context

Remote access (a paired iPhone, linked computers, remote video) listened
only on Tailscale's addresses, and a computer asking to link was refused
unless Tailscale said it belonged to your own Tailscale user. People who
don't use Tailscale, or use their own WireGuard VPN, or just want their
phone and laptop to reach the Mac on the same Wi-Fi, couldn't use any of it.

None of the protection needed Tailscale: the certificate is pinned,
devices and computers sign in with their Ed25519 keys, and linking's code
check stops anyone in between. Tailscale limited who could knock, found
your computers for you, and gave them addresses that never change.

## Decision

- **Chosen networks.** Remote access listens on the networks a person
  chooses: Tailscale (one network, `tailscale`, whatever its addresses), and
  any other network by its subnet (`192.168.1.0/24` on Wi-Fi, `10.8.0.0/24`
  on a WireGuard utun). A subnet, not an interface: a VPN's utun is
  numbered afresh when it reconnects, and Wi-Fi's en0 is the same interface
  at home and at a café, which on another subnet isn't the network chosen.
  Whenever this computer is on a chosen network, remote access listens on
  its address there (checked every few seconds); when it leaves, it stops.
  Only IPv4 for local networks and VPNs (a local network's IPv6 addresses
  change, and its subnets are the internet's); Tailscale's IPv6 too.
- **Config.** `remote-network` (repeatable; unset is Tailscale only, as
  before, so nothing changes for people already using it; set to nothing
  is none) and `remote-address` (repeatable: extra `host[:port]` to give
  out: a DNS name, a VPN endpoint, a router's forwarded port) in
  `~/.config/hammock/config`. The daemon writes them (`SetRemoteNetworks`,
  local clients only) and reads them, so a Linux hmkd works without the
  app. Settings ▸ Devices ▸ Remote Access, `hmk remote networks|enable|disable|address`
  and the TUI's Remote Access Networks edit them. Turning remote access on,
  pairing or linking with no chosen network here asks which, the network
  you're on suggested.
- **Who may connect.** Each listener is bound to a chosen network's
  address and to its interface (`IP_BOUND_IF`, `SO_BINDTODEVICE`: an
  address alone is reached from any network this computer is on), so a
  connection that arrived there came over that network. At a Tailscale
  address it must come from Tailscale; at a local network's, from its
  subnet, unless extra addresses are given out (a router's forwarded port
  brings others). Then, as before, it must sign in within seconds, with
  little allowed until it does, and only so many may wait. Anyone can make
  a key, so a computer that signs in with one but isn't linked or a member
  (a stranger) stays under those limits, and is dropped once it has
  nothing under way (no link waiting on its code, no request to join).
  Unchoosing a network, or leaving it, ends the connections that came over
  it.
- **Who may ask to link.** Over Tailscale, still only your own Tailscale
  user's computers (a tailnet shared with others is theirs too), and none
  when Tailscale can't be asked. Anywhere else, a computer may ask only
  while a person on the asked one is linking too: Link a Computer open, or
  `hmk link --accept` / `--invite`, in the last ten minutes. The code check
  stops someone in between when you started the link on both; it does
  nothing against a stranger who starts one, whose request would be a
  prompt to click through. So a request nobody started is refused, and
  never reaches a screen (it's audited). The code check shows where a
  request came from: Tailscale's host and login, or the address and
  network ("192.168.1.30 on Wi-Fi"). Asking stays limited. A computer's
  addresses are told only to its paired devices and linked computers.
- **Finding each other.** Link a Computer lists your tailnet's computers
  (as before) and Hammock found by Bonjour (`_hammock._tcp`) on the chosen
  local networks this computer is on (one found both ways, by its
  certificate, is listed once); over a VPN, which carries no multicast, you
  type its address. While remote access is on, each computer advertises
  itself on its chosen local networks only (through mDNSResponder on a Mac,
  Avahi on Linux), with its name and port and nothing else. Phone pairing
  stays a QR code.
- **Addresses move.** A computer gives out its addresses on each chosen
  network (Tailscale's first: they work from anywhere; then VPNs'; then
  local networks'; then the extra ones), with the subnets of the local
  networks among them (`lan_subnets`), in its pairing code, its link
  invitation, its record (linked computers) and its Hello reply (a paired
  phone keeps them: where to find it next time). Whoever dials tries them
  all at once, a moment apart, and keeps the first that answers with the
  pinned certificate. An address on a local network is tried only from
  that network (an address of the dialler's own in its subnet), so nothing
  is sent to whoever has that address on another network; the others are
  tried from anywhere. A phone on Wi-Fi also looks for the Mac by Bonjour.
  A certificate that doesn't match is taken as "its certificate changed"
  only at a Tailscale address: a local network's or a VPN's may be another
  computer's by now, or, away from that network, anyone's.
- **Choosing.** The first time, Tailscale is suggested if this computer is
  on it; a local network or a VPN never is (the Wi-Fi it's on may be a
  café's), nor a network on a public address.

## Consequences

- No Tailscale needed for any of it, and nothing changes for people using
  it.
- A network chosen at home is listened on at a café whose Wi-Fi happens to
  use the same subnet; whoever's there meets the pinned certificate, the
  sign-in and the code check, as anyone on your tailnet did.
- Linking off Tailscale needs you at both computers (Link a Computer open
  on each), as the code check already did.
- Shared Groups (guests, ADR-0008) still invite over Tailscale only; giving
  them the chosen networks too is a follow-up.
- A computer on a public address warns that anyone who can route to it
  could try to connect.

## As built (2026-11-10)
Changing the chosen networks, or turning remote access on, from `hmk` or the TUI is a person-only action: it becomes a request you allow in the Mac app or on your phone (tech spec §5.6). A network added to remote access by editing the config file shows a warning in Needs You.
