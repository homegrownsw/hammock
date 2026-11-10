# ADR-0002: All pane content lives on the server host

Status: Accepted · 2026-10-29

## Context
Panes could be server resources (clients are views) or client-local (e.g. a WKWebView in the Mac app).

## Decision
Every pane kind is a resource owned by the daemon on the host. Clients render streams or shared surfaces.

## Consequences
- Uniform semantics for reconnect, multiple clients, MCP control, and multiplayer.
- Visual panes need a delivery pipeline (see ADR-0005) instead of native in-client views.
- Browser and simulator panes keep running when clients quit.

## As built (2026-11-10)
Holds for every pane kind built: terminal, browser, simulator, log and editor (the daemon keeps each file's buffer, shared by every client).
