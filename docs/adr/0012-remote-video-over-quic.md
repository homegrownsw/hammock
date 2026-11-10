# ADR-0012: Remote video for visual panes goes over QUIC, not WebRTC

Status: Accepted · 2026-10-31 · supersedes the transport in ADR-0005's remote tier

## Context
ADR-0005 and the tech spec (§7.1) chose VideoToolbox encode over WebRTC (pion in the daemon, "Apple's WebRTC framework" in the apps) for showing browser and simulator panes on the iPhone and to guests. That was written before remote access settled on Tailscale (ADR-0007) and peer links on WebSocket over TLS (ADR-0008), and Apple ships no public WebRTC framework: the iPhone would need Google's libwebrtc. Under Tailscale, WebRTC's NAT traversal buys nothing; its remaining strengths are loss tolerance and rate control.

Options weighed (2026-10-31): H.264 over the existing WebSocket link (no new dependencies, but TCP stalls on loss); QUIC (quic-go in the daemon, Network.framework's built-in QUIC on iOS); WebRTC. See [the spike](../internals/remote-media.md).

## Decision
- **Codec:** H.264 from VideoToolbox in low-latency mode (no frame reordering, real-time), encoded in the helper that owns the pixels (Hammock Simulator, Hammock Browser), one session per viewer while that viewer watches. Hammock Simulator encodes its framebuffer IOSurface; Hammock Browser decodes Chromium's DevTools screencast (no Screen Recording permission; see the spike notes) and encodes that.
- **Transport:** QUIC on the remote listener's addresses (UDP, Tailscale only), ALPN `hmk-media/1`, the daemon's pinned certificate. One server-initiated unidirectional stream per frame, so a lost packet delays only its own frame, and a late frame can be abandoned. A bidirectional control stream carries the ticket, acks, size changes and keyframe requests.
- **Auth:** no second sign-in. A client already authenticated on its control link asks `OpenMedia{pane}`, which runs the usual guest checks and returns a single-use ticket bound to that connection; the media session ends with it.
- **Sender policy:** the daemon adapts the encoder's bitrate to queueing delay (frame ack latency), skips frames while ~100 ms is in flight, and abandons a frame stuck past a deadline, recovering with an LTR refresh: a P frame predicted from a long-term reference the viewer is known to have, with everything before it (a keyframe if there is none).
- **In-band fallback:** the same frames can travel as `MediaFrame` messages on any daemon connection. A guest's daemon uses it to hand frames to its own app over the local socket, and it is the path a future relay (WebSocket/HTTPS) takes.
- Guests: the guest's daemon is the QUIC client to the host and passes frames to its app in-band.

## Consequences
- No new iOS dependency; quic-go in the daemon.
- Rate control and recovery are ours to maintain. QUIC's congestion control (Cubic in quic-go) is loss-based, so random loss caps throughput as it would for TCP; the bitrate must follow.
- Network.framework quirks found in the spike are load-bearing (see the spike notes): an empty stream opened before ours, no MAX_STREAMS updates (so a huge initial stream limit), sends that close the stream unless `isComplete: false`.
- A relay must forward UDP, or fall back to in-band frames over its WebSocket.
- Not measured: QUIC against TCP under real loss. Revisit if phone streaming on cellular disappoints; the transport is behind one interface.
