# ADR-0001: Go for the daemon, CLI, and TUI

Status: Accepted · 2026-10-29

## Context
The daemon is a long-lived, highly concurrent orchestrator: protocol server, process supervision, state store, MCP, hook ingest, WebRTC. Heavy platform-specific work lives in native helpers anyway (CEF in C++, capture/XPC in Swift, libghostty in Zig/C). Rust and Zig were considered.

## Decision
Write `hmkd`, `hmk` (CLI + TUI), and initially `pane-holder` in Go. Embed libghostty-vt via cgo.

## Consequences
- Fast iteration and a simple concurrency model; pion provides mature pure-Go WebRTC.
- cgo complicates cross-compiles; libghostty must be built with Zig per target in CI.
- cgo call overhead is negligible when PTY output is fed in batches per read; GC pauses are far below the 8 ms latency budget.
- The per-pane holder's Go runtime footprint (~3–6 MB) may exceed budget; the holder is kept tiny so it can be rewritten in Zig/C if needed.
- Rust would give zero-cost FFI and tighter memory control, but slower iteration for orchestration-heavy code that isn't on the hot path.

## As built (2026-11-10)
- `hmkd`, `hmk` and `pane-holder` are all Go; the holder was never rewritten.
- No WebRTC: remote video went over QUIC instead (quic-go, [ADR-0012](0012-remote-video-over-quic.md)), so pion isn't used.
- libghostty-vt is built with Zig from the pinned commit: for macOS in CI, and cross-compiled for Linux (amd64, arm64) at release.
