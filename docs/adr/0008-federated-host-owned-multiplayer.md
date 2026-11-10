# ADR-0008: Host-owned, federated multiplayer Groups

Status: Accepted · 2026-10-29

## Context
Shared Groups could have panes on one host, on several hosts, or have guests connect as plain clients.

## Decision
A shared Group lives entirely on the host's daemon. The guest's daemon connects as a peer and proxies the Group into the guest's clients. Roles: viewer, driver, admin; the host has a kill switch. Presence shows location, typing, and follow mode.

## Consequences
- One trust boundary and routing model; guests see shared and own Groups in one UI.
- No mixed-host Groups (deferred).
- Agent MCP tokens never cross peer links.

## Update 2026-10-30: the first version

- Peer links reuse the remote listener (WebSocket + TLS 1.3, pinned self-signed certificate) and sign the server's nonce with the guest Mac's Ed25519 user key, so a future relay only forwards ciphertext and the protocol doesn't change.
- Roles are View and Drive; only the host administers. A reusable invite per Group; every person is approved by the host once per Group.
- A guest's agents (MCP, and the CLI run inside panes) are kept out of shared Groups unless the host allows it per Group.

## As built (2026-11-10)

- Guests' agents: the per-Group allow above was removed. A guest's agents act in a shared Group with their person's role (Drive means Drive), within the usual agent scope.
- Not built: follow mode ([#3](https://github.com/homegrownsw/hammock/issues/3)) and Groups spanning hosts ([#4](https://github.com/homegrownsw/hammock/issues/4)).
- Guests are invited over Tailscale only, though remote access itself now works on other networks ([ADR-0016](0016-remote-access-on-any-network.md)).
- Your own computers link as owners of each other's Groups over the same peer links ([ADR-0013](0013-linked-computers.md)).
