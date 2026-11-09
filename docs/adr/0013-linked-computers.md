# ADR-0013: Linked computers

Status: Accepted · 2026-11-02

## Context

People use Hammock on more than one computer (a laptop and a Mac Studio, a
Linux box running hmkd) and want to see and work with every computer's
Groups from whichever one they're at. Shared Groups (ADR-0008) already
proxy another daemon's Groups into your clients, but per Group, with a
person's approval each time, and as a guest.

## Decision

Link your own computers as peers of each other, each seeing every other's
Groups as their **owner**.

- **Identity.** Each computer keeps its own Ed25519 user key. Linking
  records the other's key as "also me" (a `Computer` record), so a lost
  computer can be unlinked alone and presence can tell them apart. No key
  is copied between computers.
- **Pairing.** `DiscoverComputers` lists the computers on your tailnet
  owned by your own Tailscale user (`tailscale status --json`, same
  `UserID`) and whether Hammock answers on each; a `hammock://link/` link is the
  fallback. The asking computer connects to the other's remote listener
  (learning its certificate), both sign in with their keys (PeerHello /
  PeerWelcome), and then a ZRTP-style short code: the asker commits to a
  nonce (`PeerLinkHello`), gets the other's (`PeerLinkNonce`), reveals its
  own (`PeerLinkReveal`), and both show a 6-digit code from both keys,
  both certificates and both nonces. Someone in between has different keys
  or certificates on each side, and can't choose them to make the codes
  match because the asker's nonce was fixed first. Each side links only
  once its person says the codes match (`RespondLink`, `PeerLinkConfirm`).
  Linking turns remote access on (Tailscale only), and it stays on while
  any computer is linked.
- **Only your own Tailscale user's computers can ask.** The asked computer
  looks up where the request came from in `tailscale status` and refuses it
  unless that node belongs to its own Tailscale user (on a tailnet shared
  with other people, theirs can't ask). The code check shows the Tailscale
  host and login it came from, not only the name it gives itself. Asking is
  limited (once per key or address every few seconds, a few waiting at
  once), and a new ask from the same computer replaces its last.
- **Scope: owner.** A linked computer's scope is every Group (`ROLE_OWNER`):
  everything a driver may, plus its Groups' admin (rename, pictures, close,
  new Groups there), its session finder and its folders. Not its devices,
  agents' grants, sharing or remote access, which stay on each computer.
  The host checks this per message (`ownerCheck`); a linked computer can't
  relay through one computer to a third.
- **Membership is direct links only.** Each computer keeps its own list of
  the computers it's linked with: each one's key, its pinned certificate,
  the link's id (the same on both ends) and how the link was made (by a
  person, or introduced by a computer it was linked with then). Nothing is
  gossiped and there's no graph to walk: whether B is linked with C is
  decided only by B and C, each for itself. The one thing passed on is an
  unlink, below.
  - **Linking by a person** is the code check above, on both screens. It
    also confirms a computer that's set up again: one whose certificate
    changed shows "its certificate changed", and linking again replaces
    the pin. A person linking a computer here undoes any refusal of it here.
  - **Introductions.** When A and B link, A offers each computer it's linked
    with (C) a link with B, and B one with each of them; A offers them again
    whenever its link to one connects, and every few minutes while links
    are up, so one that failed (off, busy, refused for a while) is tried
    again until it's made. C and B then link in a handshake of their own:
    one dials the other with the certificate A sent, both prove their keys,
    and each checks the other is the one introduced. The dialler names every
    computer that introduced them to it, and the other takes any of those
    that introduced them there too and is still linked with it. They link
    only if neither has unlinked the other. If the dialler backs out after
    the other side linked, it tells it to drop that link. After that the
    B–C link stands alone. Nobody links because of an introduction from a
    computer it isn't linked with, and a computer refused here (or waiting
    for a code check) isn't introduced to anyone.
  - **Unlinking is enforced by each computer that does it.** When C unlinks
    B, C deletes the link, closes B's connections and refuses B's key for
    good. B is told, as a courtesy, when it next connects ("doesn't link
    with this computer any more"), but nothing depends on B hearing it.
    "Unlink from all my computers" is the person on one computer asking
    each computer it's linked with to unlink B. Each computer that unlinks B
    because it was asked passes the request on to the computers it's linked
    with, and every computer that had a link with B tells each computer it
    links with later. So an unlink reaches every computer that's linked,
    however they're linked and whenever they were off, and a computer that
    hasn't heard yet can't introduce B to one that has (it refuses B's key
    even if it never had a link with it). A request names where a person
    asked and how long ago (each computer adds the time it held it: clocks
    differ). That's only the asking computer's word: from the computer
    where the person asked, it's never taken as earlier than it was last
    out of touch here (one connected all along can only have asked just
    now); passed on, never earlier than that computer was linked here (if
    it is: else as the computer passing it on says). A stolen computer
    still linked could claim an earlier time, but it can already ask to
    unlink those keys itself. Requests are kept
    until delivered, however long the computer is off, and dropped only when
    the computer they're for is dropped here. Nothing is ever sent to a
    computer refused here (it may be the stolen one), but its courtesy
    "doesn't link with you".
  - **Who may ask.** A computer takes an unlink request from a computer
    linked with it (and from one that says it doesn't link with it any
    more), a few a minute; from a stranger, or any computer refused here,
    never. So if C (stolen, but still linked) asks B to unlink A, and a
    person at A then unlinks C, B doesn't take A's request: A keeps it and
    tells you to link A and B again, and that relink delivers it. B, asked
    by A, shows the conflict: "C asked this computer to unlink A, and A has
    since asked to unlink C. If C is the one you lost, link A again here."
    Earlier builds let A ask back once, in the race where A and B were
    apart; but a laptop is usually off when you unlink it, and on its
    return it would ask back on every computer, cutting off the one you
    unlinked it from. From B's side the two look the same, so nothing is
    taken back automatically: the person decides. A request is never
    dropped silently: if the other computer won't take it, this one keeps
    it and tells you to link them again; if it takes it but doesn't link
    with this one, you're told that too.
  - **Introduced by a computer since unlinked.** Each link made by an
    introduction keeps its whole chain of introducers, back to a person's
    code check: who introduced it here, who introduced it to them, and so
    on, each with when the next one along linked it (at most 8; an
    introduction with a longer chain isn't taken). When a computer hears of
    B's unlink (a person here, the computer where a person asked, or one
    passing it on, whichever comes first, and again from each that comes
    later, keeping the earliest time it's told), each link here whose chain
    has a computer taking it from B after that person asked, and each link
    those introduced, needs a code check: it shows "introduced by a
    computer since unlinked", isn't linked meanwhile, and isn't passed on.
    That holds however many computers the link passed through, with none of
    them on. Links B introduced before then stand (B was yours then).
  - **Decide from your own facts; a notice is only a pointer.** A computer
    decides whether to hold a link, or take an introduction, only from
    facts it holds itself: the refusals it knows, each with when that
    unlink was asked, and the chains of its own links. Nothing another
    computer says is a verdict. A computer that marks a link as held tells
    the computers it's linked with ("hold K for its code"), and that notice
    is only a pointer to recheck K here:
    - If this computer has its own link with K, that link's chain decides.
      Tainted here (a computer refused here took it after that one's
      unlink was asked), it's held, and passed on as any link this
      computer holds. Clean, it vouches for K: the notice changes nothing
      here, isn't recorded, and isn't passed on (the computers the sender
      is linked with have it from the sender). What K introduced here
      stands, judged on its own chains like everything else. If this
      computer later holds its link with K, from its own facts, it records
      that and passes it on then.
    - If it has no link of its own with K, nothing here vouches for K, so
      the notice is what there is: K isn't linked here by an introduction
      from then on, every link here whose chain passes through K is held,
      however it reached this one, and the notice is passed on.

    An introduction carries its chain, and isn't taken if the chain has a
    computer refused here taking it after that one's unlink was asked, or a
    computer held here (for its own chain, or on a notice with no clean
    link here to vouch for it), or if it's of a key held here. An
    introduction through too many computers to check isn't taken either,
    and you're told. Any change here should be checked against this rule:
    a fact this computer holds decides; a notice only says where to look.
  - **Leaving** ("Unlink This Computer") drops every link here and asks each
    of them to drop its link with this one, named by the link's id, so a
    late copy never ends a newer link. It isn't distrust, so they don't
    refuse its key, and linking it again with one of them (by a person)
    brings the others back through introductions.
  - **Remove** (for a computer that doesn't link with this one any more,
    or waits for its code) forgets it here only: nobody is asked anything,
    and its key isn't refused. One that waited for its code stays out of
    introductions here: only Link Again brings it back.
  - **An unlinked key stays unlinked.** Each computer keeps the keys it
    unlinked (or was asked to), and when. An introduction never brings one
    back; only a person doing the code check on this computer with that
    one does. Whenever such a key asks for anything (to link, or to join a
    Group you share), you're told plainly that it was unlinked, and when.
    Linking it again on one computer doesn't undo its unlink elsewhere:
    requests to unlink it still queued for others are delivered, and each
    of your computers refuses it until it's linked again there, by hand.
  - **One-sided links resolve.** If B dials C and C doesn't link with it
    (C unlinked it, or C's database was reset), C says so, and B shows C as
    "doesn't link with this computer any more", with Link Again and Remove:
    never as linked and online with no Groups. A link just made gets a few
    seconds for the other side to catch up (kept in memory only: after a
    restart, "doesn't link" is taken at its word).
  - **Records.** A computer's name, colour, addresses and whether its app
    is open are taken only from that computer, over its own link; the
    latest one received wins. It never sends a record with no addresses: a
    computer listening on every address advertises its Tailscale addresses,
    looked up before it links.
  - **Limits** are per computer: how many introductions, refusals and
    requests one computer may leave here, so one can't crowd out another's.
    Nothing limits what a person here asks.

  **What this means.** An unlinked computer can't come back through other
  computers' links: each of yours decides for itself, refuses a key it
  unlinked whatever any other computer says, and passes the unlink on. A
  stolen computer stays linked with each of yours until that one hears the
  unlink: at once if it's on, else when it next connects to any computer
  that has heard. Until then it is what it was, an owner: whatever it did
  while linked stands (but links it introduced after its unlink was asked
  need a code check, wherever they reached), and shows in Your Computers
  to unlink; it can also ask your computers to unlink each other, and they
  do. Once a computer has refused it, it can do nothing there but be told
  so, and ask to link (you see it was unlinked, and when). If it asked
  first, you link the computers it had unlinked again, and your unlink
  reaches them then.
- **Reopen Closed** follows the computer you closed things from: a tab or
  pane a person closed on another computer goes on this one's list as a
  marker, its place taken when the close is asked and filled in once it's
  done, naming the entry that computer keeps of it (noted as closed by this
  one, and reopened only from it; only this computer says a close is a
  person's). Reopening the marker asks for that entry: if it's gone there
  (reopened, restarted, pushed out), the next close is reopened instead, in
  the same press; if that computer is offline, the marker stays and you're
  told.
- **Answering.** A link's code is checked on the computer itself: the Mac
  app (a sheet, and Needs You), the TUI (a dialog), or `hmk link
  --accept`. Phones can't answer and aren't notified: linking needs you at
  both computers anyway.
- **Groups shared with you aren't passed on.** A computer only sends its
  own Groups; each computer joins Groups others share by itself.
- **Agents** read linked computers' panes freely (MCP `list_panes`,
  `read_pane`); typing, closing or creating there asks the person, as for
  any pane they didn't open; making Groups, agents or sessions on another
  computer is refused to agents. MCP tokens still never cross links.
- **Notifications** come from the computer used last whose app is open
  (`Computer.notifies`, live state each computer sends of itself and the
  others take from it whatever the record's age): a person's keyboard or
  mouse there, from its idle
  time while its screen is unlocked, exchanged in its record. Agents typing,
  `hmk send` and a TUI over SSH don't count. A Linux box, or a Mac running
  only hmkd, never takes it. Its sidebar has every computer's agents. Phone pushes wait until you're away from all of
  them, and a phone paired with any computer hears about all of them (one
  paired with several gets one, collapsed by the item's id).
- **UI.** Every computer's Groups are mixed into one list, those on other
  computers badged with the computer's kind (laptop, Studio, server…) in its
  colour (chosen from a palette, synced); this computer's aren't badged.
  Each computer picks its own colour; when two clash, the one with the
  greater user id picks again, so they settle however the lists arrive.
  Each computer keeps its own order. The footer becomes a row of computers
  (All, then each) to show one computer at a time; new Groups go on the one
  shown. Settings ▸ Devices ▸ Your Computers lists and unlinks them.

## Consequences

- No new transport: links are peer links (ADR-0008), with their offline
  cache, routing, video and agent views.
- Group ids stay global (each daemon makes its own), so mixed lists need no
  remapping.
- Merged project Groups (the same repository on two computers as one
  Group) and handing an agent session to another computer are follow-ups.

Amended by ADR-0015: computers may ask to link from any chosen network (over Tailscale, still only your own Tailscale user's); Link a Computer also finds them by Bonjour.
