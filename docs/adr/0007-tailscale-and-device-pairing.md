# ADR-0007: Remote access via Tailscale plus device pairing

Status: Accepted · 2026-10-29

## Context
A connection to the daemon grants a shell on the host. iOS and remote clients need access without public exposure.

## Decision
Listen only on Unix sockets and the Tailscale interface. Pair devices via QR code (Ed25519 keys, mutual auth, per-device revocation). iOS push goes through a minimal relay holding the APNs key, carrying only end-to-end encrypted payloads.

## Consequences
- Requires Tailscale for remote use until a hosted relay exists.
- First piece of cloud infrastructure (push relay) arrives with the iOS app.

Amended by ADR-0015: remote access listens on the networks a person chooses (Tailscale, a local network, a VPN), not only Tailscale.
