# ADR-0005: Two-tier delivery for visual panes

Status: Accepted · 2026-10-29

## Context
"Local must feel completely transparent." Encoding video for a client on the same machine adds latency and artefacts. Remote clients need bandwidth efficiency and congestion control.

## Decision
- Local: zero-copy GPU sharing — remote CoreAnimation layers (CAContext/CALayerHost) preferred, IOSurface over XPC as fallback; native input forwarded. TUI uses Kitty graphics with shared memory.
- Remote: VideoToolbox hardware encode (HEVC/H.264, low-latency settings) over WebRTC. (Transport superseded by ADR-0012: QUIC.)
- Browser: daemon-owned CEF helpers (outlive the app), not an in-app browser with handoff, because live page state cannot migrate between processes.

## Consequences
- Native feel locally; two pipelines to maintain.
- Reliance on private APIs (CALayerHost, SimulatorKit), isolated in helpers with fallbacks.
- CEF is a large dependency, downloaded on demand.
