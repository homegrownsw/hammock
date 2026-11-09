# ADR-0003: Per-pane holder processes

Status: Accepted · 2026-10-29

## Context
If the daemon owns PTYs directly, every daemon crash or upgrade kills all running agents. The daemon will change often during development.

## Decision
Each terminal pane's PTY and child process live in a small `pane-holder` process with a byte ring buffer and a Unix socket. The daemon reconnects to holders on restart.

## Consequences
- Seamless daemon upgrades and crash recovery.
- An extra process and IPC hop per pane; mitigated by direct holder sockets for local clients if benchmarks need it.
- Doesn't survive reboot/logout; handled by layout restore and agent auto-resume.
