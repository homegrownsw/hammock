# ADR-0010: Raw length-prefixed frames for local native clients

Status: Accepted · 2026-10-29 · Amends tech spec §5.1–5.2

## Context
Network.framework can open a stream to a Unix domain socket but cannot run its WebSocket framer over one (connection aborts during setup). URLSession's WebSocket task can't target Unix sockets either.

## Decision
The daemon's socket accepts two transports, distinguished by the first bytes of a connection:
- `PRC1` followed by messages framed as a 4-byte big-endian length + protobuf `ClientMessage`/`ServerMessage` — used by the Swift clients locally.
- Anything else is treated as HTTP and upgraded to WebSocket (`/v1`, subprotocol `hammock.v1`) — used by Go clients and, later, remote clients over TLS.

Every message (including pane output) is a protobuf envelope; pane output is a `PaneOutput{pane_id, offset, data}` rather than a raw side-channel. Overhead is a few bytes per chunk.

## Consequences
- Simplest possible local path (no HTTP/WS framing), measured at ~46 µs keystroke round trip.
- Remote clients (iOS over Tailscale) will use WebSocket over TLS on a TCP listener; same messages.

## Addendum (remote clients)

Remote native clients also use Network.framework (WebSocket + TLS 1.3 to
the Tailscale listener), not URLSession: URLSession enforces App Transport
Security, which rejects the daemon's self-signed certificate even when a
delegate accepts it. Network.framework's TLS verify block lets the
certificate pin from pairing be the sole trust decision.
