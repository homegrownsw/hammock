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

- Local UDS: file permissions (sockets 0600 in a 0700 `run` directory the daemon checks it owns). The daemon then identifies each caller from its peer pid (`peerpid_*.go`, `caller.go`) and, on macOS, its audit token as it connected.
  - **Agents** in panes are scoped (§8): found by their ancestors up to a pane's shell (an agent harness among them, or a pane an agent made, marked agent-made in the same locked step that creates it). A caller that isn't placed as yours (detached from its pane, under a multiplexer, a launchd job, an agent outside Hammock) is scoped as an agent too: in the pane it was found in by terminal or multiplexer, the pane whose holder it was reparented to (Linux holders are child subreapers) or that macOS holds responsible for it, else as "a program outside Hammock" that controls only the panes it opened. Placed as yours, for ordinary actions only, are a pane's own shell (no agent between) and a program in a terminal outside Hammock with no agent, pane or Hammock process above it (and on macOS no pane's holder responsible for it).
  - **Person-only** actions (answering grants, pairing, sharing, linking, remote access, SSH setup, saving schedules without a grant, `person.go`) are taken directly only from the Hammock app (or its menu bar item), by its code signature: this daemon's team, the app's bundle ids and the hardened runtime, checked against the audit token (`LOCAL_PEERTOKEN`, `kSecGuestAttributeAudit`), so one that connects and then execs the app in its pid fails. A development build signed ad hoc has no team to check: there it's the bundle's own program, as its Info.plist names it, in the app the daemon runs from, with the hardened runtime if the daemon has it; that bundle is yours to write, so the check is weaker there. Process ancestry, environment and terminal are never taken as proof of the person: a program of yours controls all three (`launchctl submit` of a signed app with a pty of its own, `SSH_CONNECTION` in its own environment, `setsid`, `systemd-run`, cron).
  - **From anything else** (hmk in a pane's shell or a terminal, ssh, a Linux console, the TUI, a program outside Hammock) a person-only request becomes an **approval request** (`approval.go`): a `GrantRequest` with `person` set, worded by Hammock from the message ("hmk wants to get a pairing code: anyone who has it can pair a device with Studio", "… unlink Studio", a schedule in full) with an `origin` line (the pane; else the program and its terminal; "over ssh from <addr> (as it says)"; "a program outside Hammock"; and, when something other than you in the app or the TUI typed into that pane in the last 30 seconds or since you last did, "after typing from" it: a paired device, a linked computer or guest, an agent, a program such as `hmk send`, `inputsource.go`), shown in Needs You on the Mac, as a notification, and pushed to the phone (the `grant` category: Allow / Deny behind unlock). The client is sent `ApprovalPending` and waits (hmk says so on stderr); on Allow the message is handled as if sent then, on Deny or after the grant timeout it fails. Allow once only, never remembered; withdrawn if the asker disconnects; not sent to linked computers. Only the app or a paired phone answers one; answers themselves (RespondGrant, a Hammock Key answer, `ConfigWritten`, clearing a security warning) are never asked about, so nothing can approve its own request. Agents are refused person-only actions outright, as before; a caller scoped as an agent asks like anyone. Messages without an id (streams, acks) are refused, and a few refused to agents but harmless from a terminal (typing into a browser pane, reopening a tab, reading or resuming a session, listing devices or displays) go through without asking.
  - **Bootstrap** (`bootstrap.go`): until something could answer, a non-agent caller with a controlling terminal does person-only actions directly. The window shuts for good (`bootstrap.closed` in `state.db`) the first time anything is paired or linked and the first time the app connects (by its signature); unpairing or unlinking everything, or quitting or killing the app, doesn't reopen it. Once shut, with the app not running and nothing paired, a person-only request waits for you to open the app and allow it (the grant is in the state the app gets as it connects), or times out. Where a terminal did these while the window was open is kept in `state.db` too, and the first phone or computer paired or linked after raises a Needs You item (pushed; kept across restarts): "Paired <device> from a terminal, before anything could ask you (<origin>)". Only the app clears it, or a phone paired before it was raised; never the device paired as it was, one paired since, or a linked computer. While the window is open, a caller without a terminal, or one scoped as an agent, is refused ("nothing can ask you yet"). On a Mac whose app has never been opened, and on Linux before anything is paired, a detached program with a pty of its own still gets through the window: that's the bootstrap.
  - **Unraid**: the plugin page counts as the person only with a key hmkd writes to its state folder each run (`person.key`, 0600), which `action.php` passes to hmk in its environment; the key is written, and accepted, only on Unraid (`/etc/unraid-version`). Everything there runs as root, agents included, and root reads any file: on Unraid the person boundary is nominal.
  - Pane holders accept only the daemon's own executable, plus their per-pane token; on macOS by audit token, and a hardened-runtime holder or daemon also requires the hardened runtime of a peer it takes by path (no `DYLD_INSERT_LIBRARIES`). On Linux the holder checks are by pid, a speed bump rather than a boundary against your own processes: a peer with `LD_PRELOAD`/`LD_AUDIT` or whose pid changed hands mid-check is refused, but one that execs hmkd after connecting, or runs it under ptrace, isn't caught.
  - Same-user processes can't be fully isolated from one another: this closes the cheap ways out, it isn't a sandbox. **Residual:** a program of yours can type into or drive something you then act in: TIOCSTI into a shell (where the kernel still allows it); AppleScript or accessibility driving Terminal or the Hammock app itself (TCC asks first); ptrace or `/proc/<pid>/mem` into a pane's shell on Linux with `ptrace_scope` 0; poisoning your shell's rc files, or a `claude`/`codex` launcher shim in a user-writable `PATH` directory, so what you run next runs it too; writing `state.db` directly (adding a device row, or clearing `bootstrap.closed` and every device to reopen the bootstrap window); reading `identity.key` (the state folder's Ed25519 seed, a plain 0600 file), which lets it sign in to your linked computers as this one, with all a linked computer may do there (type into panes, start agents, open tabs, save schedules, answer that computer's agents' requests, use its SSH agent): a linked computer is trusted like an SSH key, and like `~/.ssh`, anything running as you can read the key; and an agent Hammock doesn't recognise by name counts as a program of yours (scoping goes by harness names, `agentkinds.go`). It can also ask: an approval request says what it does in Hammock's words and where it came from, but the origin is what the asker says of itself.
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
- **Identity:** there are no tokens. Every pane's environment gets `HMK_PANE_ID`; the daemon works out who a caller is from its process ancestry (§5.6, `caller.go`), so an agent can't claim another pane by setting the variable.
- **Default policy** (`agentCheck`): full control of panes the agent opened; read access to every pane in your own Groups and your linked computers' panes; typing into, closing or running JavaScript in a pane it didn't open, running or pausing a schedule it didn't make, searching sessions outside its project, using a linked computer's SSH key, and anything in a Group shared with you create a `GrantRequest` shown in the app ("Allow once / Always for this Group / Deny"). Person-only actions (§5.6) are refused to agents outright; from anything else but the app they're an approval request (allow once or deny). This is a guardrail for well-meaning agents, not a sandbox: an agent runs as you and can do anything you can outside Hammock; use your harness's sandbox for a real boundary.
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
- Agent identity is host-local: a guest's agents reach a shared Group only through their own daemon's grants, and never with the host's agent scope.
- **Driving is running commands as the host.** A Driver can open tabs with any command and type into shells, so a Driver (and a linked computer, which has full control both ways) can do anything the host's user can on that computer. Viewer is the only role that isn't trusted with that.

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
- Agents identified by process ancestry and scoped by `agentCheck` (a guardrail, not a sandbox); person-only actions are taken directly only from the app (by code signature) and otherwise ask the person, in the app or on a paired phone; audit log for agent and remote actions, pruned to 90 days / 50,000 rows.
- Pre-auth hardening: one sign-in attempt per connection, rate-limited failure audits, per-address and total caps on pending TLS and QUIC connections, source checks on both.
- Peer identity: a user id is a short hash of the user's key, so every sign-in is also checked against the full key on record.
- Push payloads end-to-end encrypted; the relay sees device tokens, ciphertext, keyed-hash thread and collapse tags, and the category (join, grant, hammockkey): relay/push/README.md.
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
