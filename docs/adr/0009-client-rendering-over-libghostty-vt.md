# ADR-0009: Clients render libghostty-vt state themselves

Status: Accepted · 2026-10-29 · Amends ADR-0004

## Context
ADR-0004 assumed the Apple clients could feed bytes into libghostty's own Metal renderer. Research showed the full libghostty embedding API (`ghostty.h`) is now "libghostty-internal", only spawns its own child process (no external/manual termio backend upstream; Ghostty PR #10484 closed), and no longer builds for iOS. libghostty-vt (stable functionality, unstable signatures) is the supported embedding surface and ships a render-state API for custom renderers, plus an xcframework for macOS/iOS.

## Decision
- Clients link `ghostty-vt.xcframework` (built from the same pinned Ghostty commit as the daemon) and run a local terminal fed with the daemon's snapshot + byte stream.
- Each client draws from libghostty-vt's render state. The macOS app's first renderer uses CoreText via AppKit (`TerminalNSView`); a Metal renderer can replace it behind the same `TerminalEngine.frame()` interface if profiling demands.
- Key/paste encoding uses libghostty-vt's encoders, so programs' keyboard modes (DECCKM, kitty protocol, bracketed paste) are honoured.
- The daemon's mirror is the only party that answers terminal queries (DA, DSR/CPR…); clients never write query responses.

## Consequences
- We own glyph layout, font fallback, ligatures (off for now) and GPU work; Ghostty's renderer quality is not inherited for free.
- No dependency on Ghostty internals or forks; iOS uses the same engine.
- Snapshot format: the daemon sends VT-formatted replays (libghostty formatter, VT format with extras), not the binary snapshot format, so client and daemon only need compatible VT parsing.

## As built (2026-11-11)
Rows are laid out once by `TerminalLineLayout` (HammockKit: CoreText shapes them and picks fallback fonts, and it decides where every glyph, block and line goes) and drawn by `TerminalMetalRenderer`: glyphs rasterised by CoreText into an atlas, one instanced draw per frame ([#18](https://github.com/homegrownsw/hammock/issues/18), [docs/design/metal-renderer.md](../design/metal-renderer.md)). The Mac draws with Metal by default; `terminal-renderer = coretext` draws the same layout with CoreGraphics. The iPhone draws the same way, with Metal. Ligatures are still off.
