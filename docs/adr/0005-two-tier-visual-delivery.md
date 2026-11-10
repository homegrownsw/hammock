# ADR-0005: Two-tier delivery for visual panes

Status: Accepted · 2026-10-29 · Remote transport superseded by [ADR-0012](0012-remote-video-over-quic.md)

## Context
"Local must feel completely transparent." Encoding video for a client on the same machine adds latency and artefacts. Remote clients need bandwidth efficiency and congestion control.

## Decision
- Local: zero-copy GPU sharing — remote CoreAnimation layers (CAContext/CALayerHost) preferred, IOSurface over XPC as fallback; native input forwarded. TUI uses Kitty graphics with shared memory.
- Remote: VideoToolbox hardware encode (HEVC/H.264, low-latency settings) over WebRTC. (Transport superseded by [ADR-0012](0012-remote-video-over-quic.md): QUIC.)
- Browser: daemon-owned CEF helpers (outlive the app), not an in-app browser with handoff, because live page state cannot migrate between processes.

## Consequences
- Native feel locally; two pipelines to maintain.
- Reliance on private APIs (CALayerHost, SimulatorKit), isolated in helpers with fallbacks.
- CEF is a large dependency, downloaded on demand.

## As built (2026-11-10)
- Local: browser and simulator panes are remote CoreAnimation layers (`CALayerHost`); the IOSurface-over-XPC fallback wasn't needed. The browser runs windowed Chromium behind a hidden window ([ADR-0011](0011-browser-panes-hidden-window-host.md)).
- TUI: Kitty graphics by direct transmission of PNGs, not shared memory, since the TUI often runs over SSH. Shared memory locally is [#22](https://github.com/homegrownsw/hammock/issues/22).
- Remote: H.264 over QUIC ([ADR-0012](0012-remote-video-over-quic.md)); screens add HEVC 4:4:4 and datagrams ([ADR-0014](0014-screen-sharing-and-menu-bar.md)).
- CEF is bundled in the app for now; downloading it on demand is [#37](https://github.com/homegrownsw/hammock/issues/37).
