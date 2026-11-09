# Hammock — Technical Spec

Status: Draft v0.1 · Owner: Alex Stevenson-Price · Last updated: 2026-10-29
Companion to [product-spec.md](product-spec.md). Key decisions are recorded as ADRs in [adr/](adr/).

## 1. Architecture overview

```
                       ┌─────────────────────────── Host (macOS) ───────────────────────────┐
                       │                                                                    │
 ┌──────────────┐ UDS  │  ┌──────────────────────── hmkd (Go) ───────────────────┐   │
 │ Hammock   │◀────▶│  │ Session API (protobuf/WS)   MCP server   Hook ingest (UDS)  │   │
 │ .app (Swift) │ XPC  │  │ State store (SQLite)  Agent tracker  Notifier  Thread store │   │
 │  libghostty  │◀─────┼──│ Pane manager ── VT mirror (libghostty-vt, cgo)              │   │
 └──────────────┘      │  └─────┬────────────┬───────────────┬────────────┬─────────────┘   │
 ┌──────────────┐ UDS  │        │            │               │            │                 │
 │ hammock TUI (Go)│◀────▶│  ┌─────▼─────┐ ┌────▼──────┐ ┌──────▼─────┐ ┌────▼──────────┐      │
 └──────────────┘      │  │pane-holder│ │pane-holder│ │browser-host│ │ sim-host      │      │
                       │  │ PTY+shell │ │PTY+claude │ │ CEF (C++)  │ │ (Swift)       │      │
                       │  └───────────┘ └───────────┘ └────────────┘ └───────────────┘      │
                       └──────────────────────────┬─────────────────────────────────────────┘
                                                  │ WSS / WebRTC over Tailscale
                         ┌────────────────────────┼──────────────────────┐
                    ┌────▼─────┐           ┌──────▼───────┐       ┌──────▼──────┐
                    │ iOS app  │           │ Peer daemon  │       │ Push relay  │──▶ APNs
                    └──────────┘           │ (guest, later)│      │ (CF Worker) │
                                           └──────────────┘       └─────────────┘
```

Components:

| Component | Language | Role |
|---|---|---|
| `hmkd` | Go | Long-lived daemon. Source of truth for all state; serves clients, MCP, hooks; supervises helpers. |
| `pane-holder` | Go initially (may become Zig/C, see §4.3) | One per terminal pane. Owns the PTY master and child process. Survives daemon restarts. |
| `browser-host` | C++ (CEF) + thin Obj-C++ | One per browser pane (or pooled). Offscreen Chromium; exposes frames, input, CDP. |
| `sim-host` | Swift | One per simulator pane. Captures simulator display, injects input, wraps `simctl`. |
| Hammock.app | Swift / SwiftUI + AppKit | macOS client. Embeds libghostty for terminal rendering. Onboarding/installer. |
| Hammock iOS | Swift / SwiftUI + UIKit | iOS client. Embeds libghostty. |
| `hmk` | Go | CLI and TUI client (same binary). |
| `push-relay` | TypeScript (Cloudflare Worker) | Holds APNs key; forwards opaque encrypted payloads. |

### 1.1 Language choice

Go for the daemon, CLI, and TUI (ADR-0001). The daemon is mostly concurrent I/O, process supervision, protocol, and orchestration — Go's sweet spot — and the platform-specific heavy lifting already lives in native helpers (C++ CEF, Swift capture/XPC, Zig/C libghostty). Pion gives a mature pure-Go WebRTC stack. The costs are cgo for libghostty-vt (call overhead is negligible when bytes are batched per read) and a cgo-aware cross-compile; GC pauses are sub-millisecond and well inside the 8 ms latency budget. The one place Go's footprint matters is the per-pane holder; see §4.3.

## 2. Repository layout (monorepo)

As built (one Go module at the root):

```
hammock/
  proto/                 # protobuf schemas (source of truth for the wire protocol)
  cmd/hmkd/       # daemon entry point
  cmd/pane-holder/       # per-pane PTY holder
  cmd/hmk/              # CLI, attach, hook forwarder, MCP server
  internal/daemon/       # model, panes, conns/transports, agent hooks + heuristics
  internal/holder/       # holder protocol, ring buffer, server/client
  internal/vt/           # VT mirror (go-libghostty) + raw fallback, OSC tracking
  internal/state/        # SQLite store
  internal/procinfo/     # cwd / child processes (libproc, /proc)
  internal/client/       # Go client library
  internal/proto/        # generated Go protobuf
  helpers/browser/       # CEF browser-host
  helpers/sim/           # Swift sim-host
  apple/                 # SwiftPM package: HammockKit (protocol, model, TerminalEngine), HammockMac, probe
  relay/push/            # push relay worker
  third_party/ghostty/   # pinned libghostty source + build scripts (Zig)
  bench/                 # latency/throughput harness
  docs/
```

## 3. Data model

```
Host 1─* Group 1─* Tab 1─* Pane
Pane (kind=terminal) 0..1─ Agent ─* ThreadLink ─1 Thread
Thread 1─* ThreadRecord (append-only)
Device (paired client) ; Grant (MCP/user permission) ; AuditEvent
```

Key fields:

- **Group** `id (ULID), name, order, created_at, template_ref?, last_focused_pane_id`
- **Tab** `id, group_id, name, order, layout (split tree JSON)`
- **Pane** `id, tab_id, kind (terminal|browser|simulator|log), spec (kind-specific: command, env, cwd, url, sim UDID…), runtime (holder pid, socket path, cols/rows, active_driver_client_id), cwd (live, from OSC 7 / proc)`
- **Agent** `pane_id, harness (claude|codex|unknown), vendor_session_id, state, state_since, last_summary, thread_id`
- **Thread** `id (UUIDv7), origin_harness, title, cwd, created_at, parent_thread_id?`
- **ThreadRecord** `thread_id, seq, kind (user_msg|assistant_msg|tool_call|tool_result|edit|note), payload (CBOR), content_hash`
- **Device** `id, name, platform, public_key (Ed25519), last_seen, revoked`
- **Grant** `subject (agent token | device | peer user), scope (group/pane), actions[], expires_at?`
- **AuditEvent** `ts, actor, action, target, details`

Storage: SQLite (WAL) at `~/Library/Application Support/Hammock/state.db`. All mutations go through a single writer goroutine that appends to an in-memory event bus; clients subscribe to the bus (see §5.3). Scrollback is **not** in SQLite — it lives in the holder (§4.3) with periodic snapshots to `…/scrollback/<pane>.snap` for reboot restore.

Templates: `*.hammock.toml` parsed into the same Group/Tab/Pane spec structures.

```toml
name = "web-dev"
[[tab]]
name = "main"
layout = "vsplit(60%, agent, hsplit(server, tests))"
[tab.panes.agent]
command = "claude"
[tab.panes.server]
command = "pnpm dev"
[tab.panes.tests]
command = "pnpm test --watch"
```

## 4. Terminals

### 4.1 VT engine

The daemon embeds **libghostty-vt** (Ghostty's C-ABI terminal state library, built with Zig) via cgo. Each terminal pane has a server-side **VT mirror** that consumes every byte of PTY output. The mirror provides:

- Snapshot on client attach (screen + scrollback window + modes + cursor, serialized as a replay byte stream the client's libghostty can consume).
- Screen/scrollback reads for MCP (`pane.read`), search, and agent heuristics.
- Size/mode tracking for multi-client arbitration.

libghostty source is pinned in `third_party/ghostty` and built as a static library per target. CI builds darwin/arm64, darwin/amd64, linux/amd64, linux/arm64 to keep the code portable.

### 4.2 Client rendering

Clients receive **snapshot, then PTY bytes** (ADR-0004) and feed them into their own libghostty-vt terminal, drawing from its render state (ADR-0009):

- macOS/iOS: `TerminalEngine` (HammockKit) over `ghostty-vt.xcframework`; the Mac app draws with CoreText (Metal later if needed). The daemon mirror alone answers terminal queries.
- TUI: its own libghostty-vt instance per pane; a compositor draws pane cell grids plus chrome into the host terminal.

Lockstep guarantee: daemon and clients pin the same libghostty version (protocol handshake includes the VT engine version; a mismatch forces a snapshot resync rather than streaming). Each stream frame carries a byte offset; on gap or reconnect the client requests a resync from the last acknowledged offset (served from the holder's ring buffer) or a fresh snapshot.

### 4.3 Pane holder

To survive daemon upgrades and crashes (ADR-0003), each terminal pane's PTY lives in a `pane-holder` process:

- Spawned by the daemon in its own session (`setsid`), so the daemon's going doesn't take it down; launchd-session lifetime.
- Ends when the daemon closes its pane, or stops with `ShutdownDaemon.end_panes`; 24 h after its child exits with no client (`--idle-exit`); and an hour after the app it runs from is deleted with no client (`--orphan-exit`). A starting daemon ends holders in its `run/panes` it has no pane for, and one it can't reconnect to before replacing it. In the app it's `Helpers/Hammock Pane.app`, so each shows in Activity Monitor as Hammock Pane.
- Owns the PTY master, the child process, and a **byte ring buffer** (default 8 MB) of recent output with monotonic offsets.
- Listens on a per-pane Unix socket `…/run/panes/<pane-id>.sock` (0600).
- Protocol to daemon: tiny framed binary — `Output{offset, bytes}`, `Input{bytes}`, `Resize{cols, rows}`, `Signal{n}`, `Exit{status}`, `Replay{from_offset}`.
- On daemon start: enumerate sockets, reconnect, `Replay` from the last persisted offset to rebuild VT mirrors.
- Footprint target: < 2 MB RSS. A Go holder will likely sit around 3–6 MB; acceptable for dogfood. If per-pane budget (§8 of product spec) is exceeded, rewrite the holder in Zig or C — its surface is deliberately tiny and stable for this reason.

On reboot holders are gone. The daemon restores from SQLite + scrollback snapshots, respawns shells in the recorded cwd, and auto-resumes agents (§6.4).

### 4.4 Size arbitration

PTY size = size reported by the **active driver**: the client that last sent input or explicit focus to the pane. Other attached clients render at that size, letterboxed or scaled to fit. Resizes are debounced (~50 ms) to avoid thrash when control changes.

### 4.5 Local fast path

The local client path is: client → Unix domain socket → daemon → holder socket → PTY. To meet the Ghostty-parity budget:

- Input frames are forwarded without waiting on state-store writes.
- Output fan-out to clients happens before VT-mirror parsing completes (the mirror runs on its own goroutine per pane).
- Optional optimisation if benchmarks demand: the daemon brokers a **direct holder socket** to a local client (fd passed via SCM_RIGHTS), taking the daemon out of the byte path entirely; the daemon still taps output for the mirror via a second holder subscription.

## 5. Client ↔ daemon protocol

### 5.1 Transport

- **Local:** Unix domain socket `~/Library/Application Support/Hammock/run/hmkd.sock`, carrying either raw length-prefixed protobuf frames (`PRC1` preamble; native clients) or WebSocket (ADR-0010).
- **Remote:** WebSocket over TLS on the networks chosen for remote access only (Tailscale, a local network or a VPN, by subnet: ADR-0015), bound to this computer's address on each.
- One connection per client, multiplexed into **channels**: channel 0 = control (RPC + events), channel N = per-pane streams.

### 5.2 Framing

Every transport message is one protobuf `ClientMessage` / `ServerMessage` (schemas in `proto/`, generated for Go with `protoc-gen-go` and Swift with `swift-protobuf`). Pane output travels as `PaneOutput{pane_id, offset, data}`. Raw-frame transport: 4-byte big-endian length + message; WebSocket transport: one binary message per protobuf message.

### 5.3 Control plane

- **RPC:** request/response with correlation IDs. Examples: `CreateGroup`, `CreatePane`, `SplitPane`, `ClosePane`, `AttachPane`, `DetachPane`, `SendInput`, `Resize`, `ApproveAgentPrompt`, `PairDevice`.
- **Events:** after `Subscribe{since_version}`, the daemon streams an ordered log of state deltas (`GroupUpdated`, `PaneUpdated`, `AgentStateChanged`, `AttentionItemAdded`, `PresenceChanged`…), each with a monotonically increasing state version. On reconnect, a client resumes from its last version or gets a full snapshot if too far behind.

### 5.4 Data plane (pane streams)

- Clients attach only to panes they are displaying. Hidden tabs are not streamed.
- Terminal: snapshot then byte stream with offsets; client acks offsets periodically for flow control. If a client falls behind by more than a threshold, the daemon drops the stream and sends a fresh snapshot instead of buffering unboundedly.
- Visual panes: see §7.

### 5.5 Versioning

Handshake: `Hello{protocol_version, vt_engine_version, client_kind, device_id, capabilities}`. Protobuf field evolution rules; additive changes only within a major version.

### 5.6 Authentication

- Local UDS: file permissions (0600, owned by the user) plus peer-credential check (`getpeereid`).
- Remote: device pairing. The Mac app shows a QR code containing the host's addresses on its chosen networks (ADR-0015), host public key, and a one-time pairing secret. The device generates an Ed25519 keypair, proves possession of the secret, and registers its public key. Every connection is mutually authenticated (pinned host key; device signs a challenge). Devices are individually revocable.

## 6. Agents

### 6.1 Adapters

Each harness has an adapter implementing:

```go
type Harness interface {
    Name() string
    Detect(proc ProcInfo) bool                 // is this process this harness?
    InstallHooks(ctx) error                    // idempotent; writes harness config
    LaunchCommand(spec AgentSpec) []string     // incl. resume
    ResumeCommand(vendorSessionID, cwd string) []string
    ParseHookEvent(raw []byte) (AgentEvent, error)
    TranscriptPath(vendorSessionID string) string
    Codec() ThreadCodec                        // for switching (§6.5)
}
```

v1 adapters: **Claude Code** and **Codex**.

### 6.2 State detection

1. **Hooks (primary).** Installed with `hmk hooks install` (merges into `~/.claude/settings.json` and `~/.codex/hooks.json`, turning on Codex's `[features] hooks`). Onboarding installs hooks into each harness's config (with consent and a diff preview). Hooks run `hmk hook <harness> <event>`, which reads the event JSON from stdin and posts it to the daemon over `…/run/hooks.sock`, tagged with `HMK_PANE_ID` (injected into every pane's environment). Mapping (Claude Code examples): `SessionStart`→`starting`, `UserPromptSubmit`/`PreToolUse`→`working`, `Notification` (permission/question)→`needs_input`, `Stop`→`idle`, `StopFailure` (an API error ended the turn)→`errored`, `SessionEnd`→`exited`. Codex: the same events from its hooks.json (`PermissionRequest`→`needs_input`); an older install's `notify` events map equivalently.
2. **Heuristics (fallback).** Implemented: when no hook has arrived for 2 minutes the daemon looks for a `claude`/`codex`/`opencode`/`gemini` process under the pane's shell and infers working / needs input / idle from the bottom of the mirrored screen. Process tree inspection on the pane's foreground process group, terminal title/OSC sequences, and screen pattern matching on the VT mirror (e.g. permission prompt shapes). Used for unknown agents and as a cross-check when hooks go silent.
3. **Exit status** from the holder for `errored`/`exited`.

### 6.3 Attention and notifications

- An `AttentionItem` is created on `needs_input`, `idle` (after `working`), and `errored`.
- **Presence model:** each client reports `{visible_pane_ids, last_input_at, app_foreground}`. The notifier:
  1. drops the notification if any client has the pane visible and foregrounded;
  2. else targets the device with the most recent `last_input_at` within 2 minutes, choosing a Mac banner (via the app, or `UNUserNotificationCenter` from a tiny helper if the app is closed) or iOS push;
  3. else sends iOS push to all paired iOS devices.
- **Push relay:** the daemon encrypts `{pane_id, agent, state, summary}` with a per-device key exchanged during pairing, then POSTs `{device_token, ciphertext}` to the relay. The relay (Cloudflare Worker holding the APNs auth key) sends a `mutable-content` push; the iOS Notification Service Extension decrypts and renders it. Actionable categories (Approve/Deny/Reply) call back to the daemon over Tailscale when the action runs.

### 6.4 Auto-resume after reboot

On cold start the daemon finds panes whose agent had a `vendor_session_id`, respawns a holder with `ResumeCommand(...)` in the recorded cwd, and posts an `AutoResumed` banner event. Settings toggle: `restore.auto_resume_agents = true`.

### 6.5 Threads and switching (post-v1)

- **Capture:** the daemon watches each agent's transcript (`TranscriptPath`) with FSEvents and appends normalized `ThreadRecord`s (content-hashed, append-only, CBOR) to the thread store. Records are designed to be sync-ready (UUIDv7 IDs, hash chains) but v1 is local-only.
- **Codecs:** `ThreadCodec` per harness implements `Decode(native) → []ThreadRecord` and `Encode([]ThreadRecord) → native session files`. Tool calls map to the target's equivalent tool where one exists; otherwise they become textual notes. Reasoning and images are dropped; tool I/O is clipped (e.g. 64 KiB head+tail). Lineage is recorded using native parent fields where supported.
- **Switch flow:** stop the current agent (graceful interrupt, then SIGTERM) → wait for its transcript to settle → `Encode` to the target → validate (target CLI dry-load where possible; schema checks; golden-file tests in CI per harness version) → launch the target resume command in the same holder's pane (new child process, same PTY) → on validation failure, generate a **handoff brief** (via the outgoing agent or a model call) and start a fresh target session seeded with it.
- Never write to a session that its harness currently has open.
- Prior art studied: Atuin Threads (MIT).

## 7. Visual panes

### 7.1 Two-tier delivery (ADR-0005)

| Tier | When | Mechanism |
|---|---|---|
| **Local** | Client is on the host | Zero-copy GPU sharing. Preferred: remote CoreAnimation layers (`CAContext` id → `CALayerHost` in the app) — the same split Chromium uses between its GPU and browser processes. Fallback: `IOSurface` handles passed over XPC (mach ports), drawn by the client with Metal. TUI: Kitty graphics protocol with shared-memory transmission (`t=s`). |
| **Remote** | iOS, peer daemons, remote TUI | Hardware encode (VideoToolbox HEVC, H.264 fallback) tuned for low latency (no B-frames, short GOP, CBR with fast adaptation) over **QUIC**, one stream per frame (ADR-0012; WebRTC was the first plan). Rate adapted to queueing delay, late frames abandoned, keyframe on demand. Encoding only runs while a remote viewer is attached. Remote TUI: low-fps JPEG over Kitty graphics. |

XPC between the daemon side and the app is provided by the Swift helpers (Go has no first-class XPC); the Go daemon coordinates them over its own control sockets and hands the app the XPC endpoint names.

### 7.2 Input

Locally, the Mac app forwards native `NSEvent`s (keys with IME composition, scroll with phase/momentum, magnify, mouse) to the helper over XPC with minimal translation. Remotely, input travels over the control link.

### 7.3 Browser panes

- **Engine:** CEF in `browser-host` helper processes owned by the daemon (survive app quit). Offscreen rendering with accelerated shared textures (IOSurface-backed), and remote layer export for the local tier.
- **Agent control:** each browser-host enables CDP on a private Unix socket/loopback port; the daemon proxies a scoped subset to MCP (`browser.navigate`, `browser.click`, `browser.type`, `browser.eval`, `browser.screenshot`, `browser.console`, `browser.network`).
- **DevTools:** opened as a second browser pane pointed at the CDP frontend.
- **Profiles:** one persistent dev profile per host by default (open question: per-Group profiles).
- Bundle size: CEF is large (~200 MB); shipped as an on-demand download the first time a browser pane is created.

### 7.4 Simulator panes

- `sim-host` (Swift) manages a booted simulator via `simctl` and reads its display surface directly via CoreSimulator/SimulatorKit framebuffer APIs (as idb and similar tools do) — no screen-recording capture, no visible Simulator.app window required.
- Input: HID event injection (touch, keyboard, buttons) via the same private APIs.
- Private API risk: isolated in one helper with a version-check matrix per Xcode release; fallback to `simctl io screenshot` polling + `simctl` input if APIs break.

### 7.5 Log panes

Structured tailing sources (`file`, `log stream`, `simctl spawn log stream`, `adb logcat`, process stdout), parsed into records server-side; clients receive record streams, not pixels, and render natively (TUI renders as text).

## 8. MCP server

- Transports: stdio shim (`hmk mcp`) for agents launched anywhere, connecting to the daemon socket; streamable HTTP on the local socket for clients that support it.
- **Identity:** every pane's environment gets `HMK_PANE_ID` and `HMK_AGENT_TOKEN` (a macaroon-style capability token bound to `{host, group_id, origin_pane_id}`). `hmk mcp` presents it automatically.
- **Default policy:** full control of panes created by this token's lineage; read access to other panes in the same Group; nothing outside the Group. Policy is configurable globally, per Group, and per harness (`mcp.policy` in settings). Out-of-scope calls create a `GrantRequest` shown in the app ("Allow once / Always for this Group / Deny").
- **Tools (v1):** `groups.list`, `tabs.list`, `panes.list`, `pane.read{lines, scrollback}`, `pane.create{command, cwd, split}`, `pane.send{text|keys}`, `pane.wait{pattern|exit, timeout}`, `pane.close`, `group.create{template}`, `agents.list`, `agent.state`.
- Later: browser and simulator tool families.
- Every call is written to the audit log. Pane output returned to agents is labelled as untrusted content (prompt-injection hygiene), especially for shared Groups.

## 9. Multiplayer (later)

- **Federation:** the guest's daemon connects to the host's daemon as a **peer** over WSS/WebRTC (Tailscale/direct first; relay later). The peer link carries the same protocol, scoped to shared Groups. The guest daemon re-exposes the remote Group to its own clients as a proxied Group (badged).
- **Identity:** each user has a long-lived Ed25519 user key. Sharing = host creates an invitation for a user key with a role. Later: SSO-backed identity via the relay.
- **Authorization:** `Grant{subject: peer_user, scope: group, actions by role}`. Viewer: attach read-only. Driver: input, create/close panes. Admin: manage grants. Host kill switch revokes all peer sessions immediately.
- **Presence:** clients publish `{group, tab, pane, typing}`; the host fans presence out to participants; ephemeral, not persisted.
- **Bandwidth:** only viewed panes are streamed per guest; terminals as byte streams, remote links compressed as one zstd stream per connection flushed per message (`internal/wirezstd`); visual panes as adaptive video with simulcast layers.
- **Relay (commercial):** a blind forwarder for E2EE peer links (Noise or MLS between user keys); the relay authenticates accounts but cannot read traffic.
- Agent MCP tokens are host-local and never valid on a peer link.

## 10. macOS integration

- Daemon binary in `Hammock.app/Contents/Library/LaunchAgents` + `Contents/MacOS`; registered with `SMAppService.agent(plistName:)` at onboarding (macOS 13+). Runs as the user; no root.
- Upgrades: the app installs a new daemon binary, then asks the running daemon to hand over (it drains client connections, persists state, exits; launchd starts the new one, which reconnects to holders).
- Sandbox: the Mac app is not sandboxed (it needs to spawn/talk to helpers); distributed via Developer ID + notarization, not the Mac App Store. iOS via the App Store.
- Permissions requested on demand: Notifications; Accessibility only if needed for simulator input fallbacks.

## 11. Performance and benchmarking

- `bench/` harness drives a real Hammock.app and standalone Ghostty side by side on CI Mac hardware:
  - **Keystroke-to-photon:** inject key events, detect the glyph via screen capture timestamped to display vsync; compare medians and p99.
  - **Throughput:** time to render `cat` of a fixed 100 MB file and a synthetic `vtebench`-style suite.
  - **Visual panes:** frame-timestamp watermarking through the IOSurface path.
- Budgets (gates on main): +≤ 8 ms median keystroke-to-photon vs Ghostty; ≥ 90% throughput; ≤ 1 frame added latency on visual panes.
- Daemon profiling: continuous pprof endpoints on the local socket in dev builds.

## 12. Security summary

- Listeners: Unix sockets (0600) and the chosen networks' addresses only (ADR-0015).
- Mutual device authentication with per-device revocation.
- Capability tokens for agents; least-privilege default; audit log for MCP and remote actions.
- Push payloads end-to-end encrypted; the relay sees only device tokens and ciphertext.
- Untrusted-content labelling for pane output returned to agents.
- Multiplayer: explicit invites, roles, kill switch, visible "who is typing".

## 13. Testing strategy

- Go unit tests per package; property tests for the VT mirror stream/resync logic.
- Holder chaos tests: kill/restart the daemon under load and assert no byte loss (offset continuity).
- Golden-file tests for agent hook parsing and thread codecs, per harness version.
- Protocol conformance suite shared by Go and Swift clients (recorded sessions replayed against both).
- UI tests for the Mac app; snapshot tests for SwiftUI views.
- Benchmarks as CI gates (§11).

## 14. Risks

| Risk | Mitigation |
|---|---|
| libghostty-vt API churn | Pin versions; thin cgo wrapper; track upstream; contribute fixes. |
| Server/client VT divergence | Same pinned engine version; offset-checked streams; resync on mismatch. |
| Private APIs (CALayerHost, SimulatorKit) break | Isolate in helpers; fallbacks (IOSurface, `simctl io`); per-OS/Xcode test matrix. |
| Harness hook/transcript format changes | Versioned adapters; golden tests; heuristic fallback; switching degrades to handoff brief. |
| CEF size/complexity | On-demand download; helper isolation; consider WKWebView-based lightweight mode later. |
| Scope creep | Phase gates in [build-plan.md](build-plan.md); "Out of scope" list in the product spec. |
| Auto-resume runs agents unattended | Clear banners; setting to disable; agents resume into their prior permission mode only. |
