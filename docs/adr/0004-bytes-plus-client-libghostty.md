# ADR-0004: Snapshot + raw bytes to clients, rendered by client libghostty

Status: Accepted · 2026-10-29

## Context
With a server-side VT mirror (libghostty-vt), clients could receive raw PTY bytes or server-computed cell-grid diffs (mosh-style).

## Decision
Clients receive a snapshot on attach, then the raw byte stream with offsets, and render with their own libghostty instance. Daemon and clients pin the same engine version; a mismatch or gap triggers a resync.

## Consequences
- Full Ghostty fidelity (graphics, ligatures, modes) and minimal bandwidth.
- Server and client emulators must stay in lockstep; offsets and resync handle drift.
- A grid-diff mode for poor networks remains a possible future optimisation.
