# Security

## Reporting a vulnerability

Please report security issues privately, through GitHub's **Report a vulnerability** button on this
repository's Security tab, not in a public issue. Include what an attacker needs (network position, a
paired device, a shared Group, code running as you) and what they get.

## What Hammock protects, and what it doesn't

Hammock runs as you, and so do your panes and agents. Its boundaries:

- **The network.** Remote access listens only on the networks you choose (ADR-0015). Every device and
  computer signs in with a key pinned at pairing or linking, over TLS 1.3 pinned to the host's certificate.
  Before signing in, a connection can only say hello, pair (with a single-use, expiring secret) or sign in,
  once.
- **Shared Groups.** A Viewer can watch; a **Driver can run any command as you** on the host. A linked
  computer has full control of yours and you of it. Removing someone or stopping sharing ends their
  streams at once.
- **Agents.** Agents in panes are scoped (`internal/daemon/caller.go`): they read your Groups and control
  what they open, and ask you before touching anything else. Answering those requests, pairing, sharing,
  linking, remote access, SSH setup and saving schedules are done directly only in the Hammock app,
  identified by its code signature. Asked from anywhere else (`hmk` in a shell or a terminal, the TUI, a
  program outside Hammock), they ask you first: allow once in the app or on your paired phone; nothing
  answers its own request. Hammock doesn't take where a process runs (its parents, terminal or ssh
  session) as proof that it's you, since a program of yours chooses all of those. Until anything could ask
  you (nothing ever paired or linked, the app never opened), a terminal of yours does them directly, and
  the first device paired after that is told; once the app has been open, they wait for you to open it. Any other program that talks to Hammock (one left running in the
  background, a launchd job, an agent outside Hammock) is scoped as an agent. This is a guardrail for
  well-meaning agents, not a sandbox: anything running as you can, in the end, do what you can (type
  into your shell, drive the app through accessibility, edit your shell's startup files or Hammock's
  state). For a real boundary use your agent harness's sandbox.
- **Notifications.** Push payloads are end-to-end encrypted per phone; the relay (`relay/push`) sees
  device tokens, ciphertext, keyed-hash tags and the category.
- **Untrusted output.** Terminal output, web pages and transcripts are untrusted: links that would open
  something other than a web page ask first, and names from other computers are stripped of control
  characters.

See [docs/tech-spec.md](docs/tech-spec.md) §5.6, §8, §9 and §12 for the details.
