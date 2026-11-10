# Hammock — Technical Spec

Status: describes the shipped code; plans are marked as such · Owner: Alex Stevenson-Price · Last updated: 2026-11-10
Companion to [product-spec.md](product-spec.md). Key decisions are recorded as ADRs in [adr/](adr/); how Hammock drives private frameworks and agents' terminals is in [internals/](internals/).

## 1. Architecture overview

```
 ┌──────────────────────────────────── This computer (macOS or Linux) ───────────────────────────────────┐
 │                                                                                                       │
 │  Hammock.app (SwiftUI) ─┐            ┌───────────────────────── hmkd (Go) ─────────────────────────┐   │
 │  Hammock Menu ──────────┤ Unix       │ protocol server · state (SQLite) · VT mirrors (libghostty-vt) │   │
 │  hmk CLI / TUI / mcp ───┤ socket ───▶│ agent tracking (hooks + heuristics) · grants · audit          │   │
 │  hmk hook (agents) ─────┘ (hooks)    │ schedules · sharing · linked computers · media hub · push     │   │
 │                                      └──┬────────────┬──────────────┬─────────────┬─────────────┬───┘   │
 │                                         │ per pane   │ one each     │             │             │       │
 │                                    Hammock Pane  Hammock Browser  Hammock      Hammock       Hammock    │
 │                                    (pane-holder) (CEF)            Simulator    Screen        Key        │
 │                                    PTY + child   Chromium         CoreSim +    display       Secure     │
 │                                                                   XCTest       capture       Enclave    │
 └──────────────────────────────────────────┬────────────────────────────────────────────────────────────┘
                                            │ WebSocket + TLS (control), QUIC (video), on the chosen networks
            ┌───────────────────────────────┼──────────────────────────────┐
       ┌────▼─────┐                 ┌───────▼────────┐             ┌───────▼──────┐
       │ iPhone   │                 │ another hmkd:  │             │ push relay   │──▶ APNs
       │ app      │                 │ linked computer│             │ (CF Worker)  │
       └──────────┘                 │ or a guest     │             └──────────────┘
                                    └────────────────┘
```

Components:

| Component | Language | Role |
|---|---|---|
| `hmkd` | Go | Long-lived daemon. Source of truth for all state; serves clients, agents' hooks and MCP; starts and supervises the helpers. |
| `pane-holder` | Go | One per terminal pane. Owns the PTY master and the child process; survives daemon restarts. In the app: `Contents/Helpers/Hammock Pane.app`. |
| Hammock Browser | C++/Objective-C++ (CEF) | One per daemon. Chromium in hidden windows, one per browser pane, all in one profile ([internals/browser-panes.md](internals/browser-panes.md)). |
| Hammock Simulator | Objective-C, Swift (XCTest runner) | One per daemon. Shows and drives iOS simulators through CoreSimulator and SimulatorKit, without Simulator.app ([internals/simulator-panes.md](internals/simulator-panes.md)). |
| Hammock Screen | Objective-C | One per daemon. Captures this Mac's displays for screen sharing and posts input (ADR-0014). |
| Hammock Key | Swift | Holds the Hammock Key, an SSH key in the Secure Enclave, and decides each signature. |
| Hammock.app | Swift (SwiftUI + AppKit) | The Mac client. Terminals drawn from libghostty-vt. Starts the daemon if none runs. |
| Hammock Menu | Swift | The menu bar item: a login item of its own that shows the server's state. |
| Hammock for iPhone | Swift (SwiftUI + UIKit) | The iPhone client, with a notification service extension. |
| `hmk` | Go | CLI, TUI (`hmk tui`), hook forwarder (`hmk hook`) and MCP server (`hmk mcp`), in one binary. |
| Push relay | JavaScript (Cloudflare Worker, D1) | Holds the APNs key and forwards ciphertext to phones ([relay/push](../relay/push/README.md)). |

On Linux (and Unraid) there are `hmkd`, `pane-holder` and `hmk` only: no browser, simulator, screen or key helpers ([linux.md](linux.md)).

### 1.1 Language choice

Go for the daemon, CLI and TUI (ADR-0001). The daemon is mostly concurrent I/O, process supervision, protocol and orchestration, and the platform-specific work lives in native helpers. The costs are cgo for libghostty-vt (call overhead is negligible when bytes are batched per read) and a cgo-aware cross-compile for Linux. The holder is Go too; its protocol is kept tiny and stable so it could be rewritten in C or Zig if its memory ever mattered.

## 2. Repository layout

One Go module at the root (`github.com/homegrownsw/hammock`):

```
proto/hammock/v1/       protobuf schema: the source of truth for the wire protocol
cmd/hmkd/               daemon entry point
cmd/pane-holder/        per-pane PTY holder
cmd/hmk/                CLI, TUI entry, hook forwarder and installers, MCP server
internal/daemon/        the daemon: model, panes, connections, agents, security, sharing, links, media, push
internal/holder/        holder protocol, ring buffer, server and client, state-dir lock checks
internal/vt/            VT mirror (libghostty-vt, build tag ghostty) and a raw fallback
internal/state/         SQLite store
internal/agents/        the agent registry: one file per agent (ADR-0015)
internal/agentview/     parsers that turn agents' transcripts into the agent view
internal/thread/        vendor-neutral threads and codecs for switching agents (ADR-0006)
internal/sessions/      the session finder's index
internal/tui/           the TUI
internal/client/        Go client library
internal/proto/         generated Go protobuf
internal/wirezstd/      zstd stream compression for remote links
helpers/                browser (CEF), simulator, screen, key, media (VideoToolbox encoder), common
apple/                  SwiftPM package (HammockKit, HammockMac, HammockMenu, HammockUpdates) and the Xcode project (apple/iOS)
relay/push/             push relay worker
third_party/            pinned Ghostty and CEF versions, licences
unraid/                 the Unraid plugin
```

## 3. Data model

```
Group 1─* Tab 1─* Pane            (Tab: a split layout tree, plus floating panes)
Pane (terminal) 0..1─ Agent       AttentionItem (Needs You) per pane
GrantRequest · Person / Member · Computer (linked) · Schedule / ScheduleRun · Device (paired) · AuditEvent
Thread 1─* Record                 (captured when a session is switched or resumed in another agent)
```

All of these are protobuf messages in `proto/hammock/v1/hammock.proto`. Ids are short random strings with a type prefix (`tb_k3x9q2ma`).

- **Group**: name, emoji or picture (from `.hammock/icon.png` in its folder), its folder (where new tabs open), its tabs, and, when shared, its members and your role.
- **Tab**: name, layout (a split tree), floating panes.
- **Pane**: kind (terminal, browser, simulator, editor; a log pane is a terminal running a log source), spec (command, cwd, env, URL, simulator UDID, log source, file), title and cwd (from OSC sequences and the process), its processes, and its agent if one runs.
- **Agent**: harness (`claude`, `codex`, and the rest of the registry), session id, transcript path, state (starting, working, needs input, idle, errored, exited) and since when.

Storage: SQLite at `<state dir>/state.db`: generic `entities` (kind, id, protobuf blob), pane runtimes (holder socket and token), devices, settings, threads, the audit log. Mutations publish `StateUpdate` deltas to subscribed clients (§5.3). Scrollback is not in SQLite: it lives in the holder (§4.3), and each pane's screen is saved as a VT snapshot under `scrollback/` for restoring after a reboot.

The state dir is `~/Library/Application Support/Hammock` on a Mac (the app's `HammockStateDir` names it, so a development copy has its own), `$XDG_STATE_HOME/hammock` (or `~/.local/state/hammock`) on Linux, or `$HMK_DIR`. It holds:

| Path | What |
|---|---|
| `state.db` | the model, devices, settings, audit log |
| `identity.key` | this computer's Ed25519 user key (0600) |
| `tls/` | the self-signed certificate remote clients pin |
| `run/` | sockets and locks: `hmkd.sock`, `hooks.sock`, `daemon.lock`, `panes/<pane>.sock`, the helpers' `browser-host.sock`, `simulator-host.sock`, `screen-host.sock`, and `ssh-agent.sock` |
| `scrollback/` | each terminal pane's last saved screen (`<pane>.vt`, `.offset`) |
| `logs/` | the daemon's and helpers' logs |
| `browser-profile/` | the one Chromium profile every browser pane shares |
| `panelogs/`, `grouppics/` | log panes' parsed entries; Groups' pictures |
| `person.key` | Unraid only (§5.6) |

Settings people edit are in `~/.config/hammock/config` (Ghostty syntax), not the state dir.

Templates (a TOML file describing a Group's tabs and commands, in the product spec) are **not built**.

## 4. Terminals

### 4.1 VT engine

The daemon embeds **libghostty-vt** (Ghostty's C-ABI terminal library, built with Zig) via cgo, under the `ghostty` build tag; without it a raw fallback keeps recent bytes for replay. Each terminal pane has a server-side **VT mirror** that consumes every byte of PTY output. The mirror provides:

- the snapshot a client gets on attach: a VT byte stream that rebuilds the screen, scrollback, modes and cursor in a fresh terminal;
- screen and scrollback text for `ReadPane`, MCP and agent heuristics;
- answers to terminal queries (colours, the light/dark scheme, cell size), from the colours the app sends (`SetColors`).

Ghostty is pinned in `third_party/ghostty` and built once per pinned commit into a cache every checkout shares. Builds cover darwin/arm64 and linux/amd64 and arm64.

### 4.2 Client rendering

Clients get **a snapshot, then PTY bytes** (ADR-0004) and feed them into their own libghostty-vt terminal, drawing from its render state (ADR-0009):

- Mac and iPhone: `TerminalEngine` (HammockKit) over `ghostty-vt.xcframework`; the Mac draws with CoreText. Only the daemon's mirror answers terminal queries.
- TUI: a libghostty-vt terminal per pane; a compositor draws the panes' cells and Hammock's chrome into the host terminal.

Both ends exchange their engine's version in `Hello`/`HelloReply`. Each `PaneOutput` carries its byte offset in the pane's output; a snapshot says the offset it was taken at, so output continues exactly after it.

### 4.3 Pane holder

So panes survive daemon upgrades and crashes (ADR-0003), each terminal pane's PTY lives in a `pane-holder` process:

- Started by the daemon in its own session (`setsid`), so the daemon's going doesn't take it down. The login item's launchd job sets `AbandonProcessGroup`, and the Linux systemd unit `KillMode=process`, for the same reason.
- Ends when the daemon closes its pane, or stops with `ShutdownDaemon.end_panes`; 24 h after its child exits with no client (`--idle-exit`); and an hour after the app it runs from is deleted with no client (`--orphan-exit`). A starting daemon ends holders in its `run/panes` it has no pane for, and one it can't reconnect to before replacing it.
- Owns the PTY master, the child and a **ring buffer** (8 MiB) of recent output with monotonic offsets.
- Listens on `run/panes/<pane>.sock` (0600), and takes only its own daemon with the pane's token (§5.6).
- Protocol: frames of 1 byte type, 4 byte big-endian length, payload: `Attach{from_offset, token}`, `Info`, `Output{offset, bytes}`, `Input`, `Resize{cols, rows}`, `Signal`, `Exit{status}`, `Close` (`internal/holder/frame.go`).
- On start the daemon reconnects to each pane's holder from its runtime record in `state.db`, rebuilds the mirror from the saved snapshot and asks the holder for the output after that offset.

After a reboot the holders are gone. The daemon restores the layout from `state.db`, seeds each pane with its saved screen, starts a fresh shell in the recorded cwd, and resumes agents (§6.4).

### 4.4 Size arbitration

A pane's PTY size is the **active driver's**: the client that last focused or typed into it. Other clients render at that size.

### 4.5 Local fast path

Client → Unix socket → daemon → holder socket → PTY. Input is written to the holder without waiting on the store. Output is parsed into the mirror and fanned out on the pane's own read goroutine; sends to clients never block (§5.4). A keystroke's round trip through daemon and holder is about 0.05 ms ([benchmarks.md](benchmarks.md)), so handing a local client the holder's socket directly hasn't been needed.

## 5. Client ↔ daemon protocol

### 5.1 Transport

- **Local:** the Unix socket `<state dir>/run/hmkd.sock`, carrying either raw length-prefixed protobuf frames (a `PRC1` preamble; the native clients) or WebSocket (ADR-0010).
- **Remote:** WebSocket over TLS 1.3 on port 7433, listening only on the addresses of the networks chosen for remote access (Tailscale, a local network or a VPN, by subnet: ADR-0016). Off by default. A remote link may negotiate zstd compression (`internal/wirezstd`: one stream per connection, flushed per message).
- **Video:** QUIC on the same addresses and port (UDP), for remote viewers of visual panes and screens (§7.1).
- One connection per client. There are no channels: every message is a whole `ClientMessage` or `ServerMessage`.

### 5.2 Framing

Each transport message is one protobuf `ClientMessage` or `ServerMessage` (generated for Go with `protoc-gen-go` and Swift with `swift-protobuf`). Raw-frame transport: 4-byte big-endian length, then the message. WebSocket: one binary message per protobuf message.

### 5.3 Control plane

- **Requests:** a `ClientMessage` carries an `id`; its reply carries it back in `reply_to` (`Ok`, `Error`, `Created` or a message of its own). Examples: `CreateGroup`, `CreateTab`, `CreatePane`, `ClosePane`, `AttachPane`, `Input`, `Resize`, `RespondGrant`, `CreatePairing`, `StartAgent`, `SaveSchedule`.
- **State:** after `Subscribe`, the daemon sends a full `StateUpdate`, then deltas (upserts and deleted ids of Groups, tabs, panes, attention items, grants, people, computers, schedules…), each with a rising version. A reconnecting client subscribes again and gets the full state.

### 5.4 Data plane

- Clients attach only to panes they show.
- Terminal: `PaneSnapshot`, then `PaneOutput` with offsets. Each client has a bounded send queue: once it's half full, a pane's output is skipped for that client and the pane's screen is sent afresh when it has caught up; a client whose queue fills is dropped and reconnects (`conn.go`).
- Visual panes: §7.

### 5.5 Versioning

Handshake: `Hello{protocol_version, client_kind, client_name, vt_engine}` and `HelloReply{protocol_version, daemon_version, host_name, vt_engine, …}`. Protocol version 1; changes stay additive (fields and messages are added, numbers never reused). The Mac app replaces a running daemon of another version with the one it bundles (§10).

### 5.6 Authentication

- Local UDS: file permissions (sockets 0600 in a 0700 `run` directory the daemon checks it owns). The daemon then identifies each caller from its peer pid (`peerpid_*.go`, `caller.go`) and, on macOS, its audit token as it connected.
  - **Agents** in panes are scoped (§8): found by their ancestors up to a pane's shell (an agent harness among them, or a pane an agent made, marked agent-made in the same locked step that creates it). A caller that isn't placed as yours (detached from its pane, under a multiplexer, a launchd job, an agent outside Hammock) is scoped as an agent too: in the pane it was found in by terminal or multiplexer, the pane whose holder it was reparented to (Linux holders are child subreapers) or that macOS holds responsible for it, else as "a program outside Hammock" that controls only the panes it opened. Placed as yours, for ordinary actions only, are a pane's own shell (no agent between) and a program in a terminal outside Hammock with no agent, pane or Hammock process above it (and on macOS no pane's holder responsible for it).
  - **Person-only** actions (answering grants, pairing, sharing, linking, remote access, SSH setup, saving schedules without a grant, `person.go`) are taken directly only from the Hammock app (or its menu bar item), by its code signature: this daemon's team, the app's bundle ids and the hardened runtime, checked against the audit token (`LOCAL_PEERTOKEN`, `kSecGuestAttributeAudit`), so one that connects and then execs the app in its pid fails. A development build signed ad hoc has no team to check: there it's the bundle's own program, as its Info.plist names it, in the app the daemon runs from, with the hardened runtime if the daemon has it; that bundle is yours to write, so the check is weaker there. Process ancestry, environment and terminal are never taken as proof of the person: a program of yours controls all three (`launchctl submit` of a signed app with a pty of its own, `SSH_CONNECTION` in its own environment, `setsid`, `systemd-run`, cron).
  - **From anything else** (hmk in a pane's shell or a terminal, ssh, a Linux console, the TUI, a program outside Hammock) a person-only request becomes an **approval request** (`approval.go`): a `GrantRequest` with `person` set, worded by Hammock from the message ("hmk wants to get a pairing code: anyone who has it can pair a device with Studio", "… unlink Studio", a schedule in full) with an `origin` line (the pane; else the program and its terminal; "over ssh from <addr> (as it says)"; "a program outside Hammock"; and, when something other than you in the app or the TUI typed into that pane in the last 30 seconds or since you last did, "after typing from" it: a paired device, a linked computer or guest, an agent, a program such as `hmk send`, `inputsource.go`), shown in Needs You on the Mac, as a notification, and pushed to the phone (the `grant` category: Allow / Deny behind unlock). The client is sent `ApprovalPending` and waits (hmk says so on stderr); on Allow the message is handled as if sent then, on Deny or after the grant timeout it fails. Allow once only, never remembered; withdrawn if the asker disconnects; not sent to linked computers. Only the app or a paired phone answers one; answers themselves (RespondGrant, a Hammock Key answer, `ConfigWritten`, clearing a security warning) are never asked about, so nothing can approve its own request. Agents are refused person-only actions outright; a caller scoped as an agent asks like anyone. A scheduled task that runs a command runs it in a pane like any other, so a person-only `hmk` command in it (`hmk pair`, `hmk schedule add`, `hmk remote on`, …) asks too: it waits for your answer and, with nobody answering (you're away, the app is closed and no phone is paired), fails after the grant timeout (two minutes); put such steps in the app, not in a schedule. Messages without an id (streams, acks) are refused, and a few refused to agents but harmless from a terminal (typing into a browser pane, reopening a tab, reading or resuming a session, listing devices or displays) go through without asking.
  - **Bootstrap** (`bootstrap.go`): until something could answer, a non-agent caller with a controlling terminal does person-only actions directly. The window shuts for good (`bootstrap.closed` in `state.db`) the first time anything is paired or linked and the first time the app connects (by its signature); unpairing or unlinking everything, or quitting or killing the app, doesn't reopen it. Once shut, with the app not running and nothing paired, a person-only request waits for you to open the app and allow it (the grant is in the state the app gets as it connects), or times out. Where a terminal did these while the window was open is kept in `state.db` too, and the first phone or computer paired or linked after raises a Needs You item (pushed; kept across restarts): "Paired <device> from a terminal, before anything could ask you (<origin>)". Only the app clears it, or a phone paired before it was raised; never the device paired as it was, one paired since, or a linked computer. While the window is open, a caller without a terminal, or one scoped as an agent, is refused ("nothing can ask you yet"). On a Mac whose app has never been opened, and on Linux before anything is paired, a detached program with a pty of its own still gets through the window: that's the bootstrap.
  - **Unraid**: the plugin page counts as the person only with a key hmkd writes to its state folder each run (`person.key`, 0600), which `action.php` passes to hmk in its environment; the key is written, and accepted, only on Unraid (`/etc/unraid-version`). Everything there runs as root, agents included, and root reads any file: on Unraid the person boundary is nominal.
  - Pane holders accept only the daemon of their own state dir: the process holding its locks (`holder/statelock.go`). hmkd takes an exclusive flock on `run/daemon.lock` (a plain file of yours with one name, opened without following links, in a real `run/` directory) and then on the state dir itself, and keeps both while it runs; it refuses to start if the lock file has a second name or something else holds the dir's lock. A holder records both files (device and inode) as it starts, with the daemon starting it holding them, and takes a client only if that process has descriptors on both, other than its stdin, stdout and stderr, through which a flock was taken (macOS: `FHASLOCK` in `proc_pidfdinfo`, which stays set once the flock is let go; Linux: the `lock:` line in `/proc/<pid>/fdinfo`, there while it's held), both are flocked now (the holder's own non-blocking flock on a fresh descriptor is refused; neither system says which process holds a flock), and `run/daemon.lock` still names that file with one link. Having the files open isn't enough (a daemon reads files whose paths agents choose, through links), and closing another descriptor doesn't drop a flock, as it did the fcntl lock this replaced. hmkd closes every descriptor it inherits above stderr before anything else, and replaces with `/dev/null` a stdin, stdout or stderr that's closed, a folder, a file a flock was taken through or anything but a device, pipe, socket or plain file (it's started with `/dev/null`, the journal or its log file), so a descriptor of the locks, flocked and let go while no daemon ran, can't be carried into it across exec and pass for its own once the real daemon has them again. A daemon reconnects only to sockets at `run/panes/<pane>.sock` in its own dir that are sockets themselves (no link, no second name), and holders with the state check serve only there. They also accept only the daemon's own executable, plus their per-pane token; on macOS by audit token, and a hardened-runtime holder or daemon also requires the hardened runtime of a peer it takes by path (no `DYLD_INSERT_LIBRARIES`). On macOS this is the boundary: a process of yours can't put its code in the signed, hardened hmkd, and that hmkd holds a state dir's locks only as its daemon; residuals known are root, and the state dir's own files, which the daemon trusts when it starts (anything of yours can change `state.db` or the config and restart it). Holders started before the state check (by builds before it) have only the program and token checks until their panes are restarted. On Linux the holder checks are a speed bump rather than a boundary against your own processes: a process of yours can ptrace an hmkd it starts (Yama's default scope 1 allows a parent its descendants) or run one with its own code preloaded, and then that hmkd is its code with the daemon's locks; a peer with `LD_PRELOAD`/`LD_AUDIT` in its environment, or whose pid changed hands mid-check, is refused, but one that execs hmkd after connecting isn't caught by the program check.
  - What a daemon runs, when a program of yours starts it: in a release (signed by a team), `--holder` and `HMK_BROWSER_HOST`, `HMK_SCREEN_HOST`, `HMK_SIMULATOR_HOST` and `HMK_KEY_HELPER` are taken only if they're inside the daemon's own app or signed by its team (checked on disk as the daemon chooses them: a team-signed copy in a folder of yours could be swapped after), and otherwise ignored with a log line, the app's own used (`daemon/helperpath.go`); `--remote-listen` is ignored (development builds only). Development builds, ad hoc or unsigned, and Linux take any. **Residual:** `SHELL` (each new pane's shell, as you'd expect from your environment), `HMK_PUSH_RELAY` (an extra https relay origin a phone may name for its notifications, beside Hammock's own), `--dir` and the state dir's own files and config: a program that starts the daemon of your state dir chooses these, as it could run the programs itself.
  - Same-user processes can't be fully isolated from one another: this closes the cheap ways out, it isn't a sandbox. **Residual:** a program of yours can type into or drive something you then act in: TIOCSTI into a shell (where the kernel still allows it); AppleScript or accessibility driving Terminal or the Hammock app itself (TCC asks first); ptrace or `/proc/<pid>/mem` into a pane's shell on Linux with `ptrace_scope` 0; poisoning your shell's rc files, or a `claude`/`codex` launcher shim in a user-writable `PATH` directory, so what you run next runs it too; writing `state.db` directly (adding a device row, or clearing `bootstrap.closed` and every device to reopen the bootstrap window); reading `identity.key` (the state folder's Ed25519 seed, a plain 0600 file), which lets it sign in to your linked computers as this one, with all a linked computer may do there (type into panes, start agents, open tabs, save schedules, answer that computer's agents' requests, use its SSH agent): a linked computer is trusted like an SSH key, and like `~/.ssh`, anything running as you can read the key; and an agent Hammock doesn't recognise by name counts as a program of yours (scoping goes by the registry's process names, `agentkinds.go`, `internal/agents`). It can also ask: an approval request says what it does in Hammock's words and where it came from, but the origin is what the asker says of itself.
- **Remote: device pairing.** The Mac app shows a QR code (`Pairing`) with this computer's name, its addresses on the chosen networks (ADR-0016), the SHA-256 of its TLS certificate, and a single-use pairing secret that expires in 10 minutes. The device makes an Ed25519 key pair and sends `Pair{secret, name, platform, public key}`. Every later connection pins the certificate, and the device signs the daemon's fresh nonce, bound to that certificate (`Authenticate`). Before signing in, a connection may send only small messages, must sign in within 15 seconds, gets one attempt, and only a few may wait at once, per address and overall. Devices are revoked one by one.

## 6. Agents

### 6.1 The agent registry

Every agent Hammock knows is one `agents.Agent` value in its own file in `internal/agents` (ADR-0015; [agents.md](agents.md) lists them and what each supports): its command and process names, how to install its hooks and read them, how to start and resume it, what its screen shows when it's working or waiting, where its session files are and how to index them, and a parser for the agent view (`internal/agentview`). The Swift side has a matching catalogue (`AgentCatalog.swift`). Claude Code and Codex support everything, switching included; other agents support what their own files and hooks allow.

### 6.2 State detection

1. **Hooks (primary).** `hmk hooks install` (or onboarding, with consent) merges Hammock's hooks into each agent's own config (for Claude Code `~/.claude/settings.json`, for Codex `~/.codex/hooks.json`), backing it up first. A hook runs `hmk hook <harness> [event]`, which posts the event to the daemon over `run/hooks.sock`; it must never block or fail the agent. The daemon takes hook events only from inside its panes, and finds the pane from the sender's process, not from `HMK_PANE_ID` (§5.6). Claude Code's mapping: `SessionStart` → starting, `UserPromptSubmit`/`PreToolUse` → working, `PermissionRequest` and a permission `Notification` → needs input, `Stop` → idle, `StopFailure` (an API error ended the turn) → errored, `SessionEnd` → exited. Other agents map their events to these names.
2. **Heuristics (fallback).** For a pane with no hook in the last 2 minutes, the daemon looks for a known agent process under the pane's shell (by executable name and path) and infers working, needs input or idle from the bottom of the mirrored screen, using each agent's own screen patterns.
3. **Exit status** from the holder for errored and exited.

The agent view (`WatchAgent`) reads an agent's transcript, a hook names, only when it is a regular session file inside that agent's own session folders (`transcripts.go`).

### 6.3 Attention and notifications

- An `AttentionItem` (Needs You) is raised when an agent needs input, finishes a turn after working, or errors; also for grants and security warnings.
- **On the Mac**, the app posts notifications for attention items, to Notification Center or as cards in the window, skipping panes you're looking at, and withdraws them once they're dealt with (`Notifier.swift`). With the app closed, the Mac doesn't notify.
- **On the phone** (`push.go`), a push waits until you're away: this Mac's screen locked or untouched for 2 minutes, or Hammock used on a paired phone since you last touched any of your computers, and not while you're at another of your linked computers. It is dropped if it stops mattering first (answered, seen, replaced) or after an hour. A phone with Hammock open in front gets none. Hammock Key requests go to phones at once.
- **Push relay** (`relay/push`): each phone registers with the relay (`/register`), gets a relay id, and gives the Mac its id, relay URL and a 256-bit key over its signed-in connection (`RegisterPush`). The Mac encrypts each notification for that phone (AES-256-GCM, with the time it was sent) and POSTs `{id, payload, thread, collapse, category}` to the relay's `/send`. The thread and collapse tags are keyed hashes of the pane's or request's id, unique to that phone, so ids never leave the Mac. The category is `join`, `grant`, `hammockkey` or none. The relay sends a `mutable-content` push through APNs; the notification service extension decrypts it and shows nothing it can't decrypt or that was sent over an hour ago. Actions (Allow / Deny for a grant, a join or a Hammock Key request) run in the app, which answers the daemon over its own connection.
- Relays: Hammock's own at `https://push.homegrownsw.com`, which also still answers at `https://hmk-push.homegrownsw.workers.dev` for phones registered before the domain; a phone may name only those, or an origin in `HMK_PUSH_RELAY` (a build of your own needs a relay of its own).

### 6.4 Resume after a reboot or restart

A pane whose holder is gone and whose agent had a session id (and hadn't exited) is started again with its agent's resume command in the recorded cwd, followed by a login shell, so the pane is a shell again once the agent exits (`agents.go`). There is no setting to turn this off.

### 6.5 Threads and switching

- **Threads** (ADR-0006, `internal/thread`): a vendor-neutral, append-only list of records (user, assistant, tool call, tool result, note, a reasoning marker), each content-hashed, stored as JSON in `state.db`. A thread is captured when a session is switched, or resumed in another agent from the session finder, not continuously.
- **Codecs:** Claude Code and Codex each have one that decodes native session files into records and encodes records into a native session. Tool calls map to the target's equivalent where there is one; otherwise they become notes. Reasoning is never carried across.
- **Switching** (`switch.go`): read the source session (failing fast), interrupt the agent until the shell comes back, encode a new session for the target, and start the target resuming it in the same pane. If converting fails, the target starts fresh with a **handoff brief** built from the records (no model call). Hammock never writes to a session its harness has open.

## 7. Visual panes

### 7.1 Two-tier delivery (ADR-0005)

| Tier | When | Mechanism |
|---|---|---|
| **Local** | The Mac app on the same computer | Zero-copy: the helper shares a CoreAnimation context and the app shows it with `CALayerHost` (Chromium's own GPU layer for a browser; a layer over the framebuffer IOSurface for a simulator). The app talks to the helper directly over its Unix socket (`run/browser-host.sock`, `run/simulator-host.sock`) for layers and input. |
| **Remote** | The iPhone, linked computers, guests | The helper encodes video with VideoToolbox (`helpers/media`; H.264, and HEVC for screens) for each viewer, and the daemon sends it over **QUIC**: one stream per frame, or datagrams with parity shards (ADR-0012, ADR-0014), or in band on the control link when QUIC can't connect. Bitrate follows queueing delay from the viewer's acks; the encoder is held while about 100 ms of video is unacked; a frame unacked for 400 ms is given up and recovery uses long-term references or a keyframe. Encoding runs only while someone watches. See [internals/remote-media.md](internals/remote-media.md). |
| **TUI** | `hmk tui` in a terminal with Kitty graphics | Screenshots polled from the daemon and drawn with Unicode placeholders, more slowly over SSH (`internal/tui/live.go`). |

### 7.2 Input

Locally, the Mac app sends native events to the helper over its socket: for a browser, the `CGEvent` bytes (phases, momentum and magnify intact) and the text input system's calls replayed inside Chromium; for a simulator, touches, gestures, buttons and keys. Remotely, input travels over the control link (`BrowserInput`, `SimulatorCall`, `ScreenInput`).

### 7.3 Browser panes

- **Engine:** CEF in Hammock Browser, one process per daemon (one profile, so one set of logins), started by the daemon and outliving the app (and the daemon) like holders. Each pane is a windowed Chromium browser in a hidden, transparent window behind the pane (ADR-0011; [internals/browser-panes.md](internals/browser-panes.md)). After a reboot the daemon reopens each at its stored URL.
- **Bundled:** CEF ships inside the app (`Contents/Helpers/Hammock Browser.app`).
- **Agent control:** the daemon reaches each page through CEF's in-process DevTools channel and exposes MCP tools (`browser_open`, `browser_navigate`, `browser_snapshot`, `browser_click`, `browser_type`, `browser_press_key`, `browser_evaluate`, `browser_screenshot`, `browser_console`, `browser_network`, `browser_wait_for`). Read-only scripts run wherever an agent may read; anything else only where it may control (§8).
- **DevTools:** opened in a pane of its own (`devtools.go`).
- **Remote video:** DevTools screencast frames, decoded and re-encoded as H.264 by the helper.

### 7.4 Simulator panes

- Hammock Simulator reads a booted device's framebuffer IOSurface through CoreSimulator, and injects touches, buttons and keys through SimulatorKit's HID client: no Simulator.app and no screen recording. Rotation, multi-finger gestures and the like go through an XCTest runner it starts per device; foldables through a helper inside the device. All of it is private API, kept in one helper ([internals/simulator-panes.md](internals/simulator-panes.md)).
- MCP tools: `sim_list_devices`, `sim_open`, `sim_screenshot`, `sim_tap`, `sim_swipe`, `sim_type`, `sim_press_button`, `sim_rotate`, `sim_fold`, `sim_pinch`, `sim_install_app`, `sim_launch_app`, `sim_terminate_app`, `sim_open_url`, `sim_logs`, `sim_shutdown`.

### 7.5 Log panes

A log pane is a terminal pane running a source (`file`, `macos` for `log stream`, `simulator`, `logcat`, or a `command`), whose output the daemon parses into entries (time, level, process, message) kept in `panelogs/`. Clients get entries, not pixels (`LogView`), and filter them; the TUI shows them as text. MCP: `log_open`, `log_read`.

### 7.6 Editor panes

An editor pane shows a text file; the daemon owns its buffer (`editor.go`), and the Mac app draws it with CodeMirror. Agents edit files on disk, never the buffer; they can open a file and point at lines (`editor_open`, `editor_select`, `editor_read`).

## 8. MCP server

- **Transport:** stdio (`hmk mcp`), connecting to the daemon's socket; registered for agents by `hmk hooks install`. Outside a Hammock pane it offers no tools.
- **Identity:** there are no tokens. Every pane's environment gets `HMK_PANE_ID`; the daemon works out who a caller is from its process ancestry (§5.6, `caller.go`), so an agent can't claim another pane by setting the variable.
- **Policy** (`agentCheck` in `internal/daemon/caller.go`, enforced by the daemon for every local connection it places as an agent, through MCP or `hmk`):
  - **read** (screens, agent views, logs, editors, read-only page scripts, simulator screenshots) every pane in your own Groups and on your linked computers;
  - **organise** your own Groups: create tabs, panes and Groups, start agents, rename, move and lay out tabs;
  - **control** (type, interrupt, close, replace, navigate a browser, run other JavaScript, drive a simulator) only panes it opened, directly or through panes it opened;
  - **ask** (`GrantRequest`, shown in Needs You and pushed: Allow once / Always for this agent and action in this Group / Deny) for control of any other pane, for anything in a Group shared with you, and for anything on another computer beyond reading; saving or deleting a schedule always asks, worded from the schedule, and running or pausing one it didn't make asks too;
  - **refused**: answering grants, and everything person-only (§5.6). Agents edit files on disk, never an editor pane's buffer.
  This is a guardrail for well-meaning agents, not a sandbox: an agent runs as you and can do anything you can outside Hammock. For a real boundary use your harness's sandbox.
- **Tools:** panes (`list_panes`, `read_pane`, `create_pane`, `arrange_pane`, `send_to_pane`, `wait_for_pane`, `close_pane`, `show_output`), sessions (`session_search`, `session_read`), schedules (`schedule_list`, `schedule_save`, `schedule_run`, `schedule_pause`, `schedule_delete`), and the browser, simulator, log and editor families (§7).
- **Audit:** what agents, MCP and remote clients change (input, creating and closing panes and tabs, navigating, answering agents, starting agents, uploads, saving editors…) and every grant decision is written to the audit log (`hmk audit`, Settings). Pane output and web pages returned to agents are labelled untrusted in the tools' descriptions.

## 9. Multiplayer: shared Groups and linked computers

- **Federation** (ADR-0008): a guest's daemon connects to the host's remote listener as a **peer** (WebSocket + TLS, the same protocol), signs in with its user key (`PeerHello`, bound to the host's certificate; the host proves itself the same way), and joins with a Group's invite secret. The host approves each person once per Group. The guest's daemon keeps a copy of the shared Groups, which its clients see among their own (dimmed while the host can't be reached), and sends what they do on to the host.
- **Identity:** each computer has a long-lived Ed25519 user key (`identity.key`). A user id is a short hash of the key, so every sign-in is also checked against the full key on record.
- **Roles:** Viewer (watch) and Driver (type, answer agents, open and close tabs), checked by the host on every message (`checkGuest` in `share.go`). A guest sees only the Groups it's a member of. Removing someone or stopping sharing (`StopSharing`) ends their streams at once.
- **Presence:** members' Group, tab, pane and typing are fanned out to participants; not persisted.
- **Bandwidth:** only viewed panes are streamed; terminals as bytes over a zstd-compressed link; visual panes as video (§7.1).
- **Linked computers** (ADR-0013, ADR-0016): your own computers, linked in pairs with a short code compared on both screens. Each sees the other's Groups as **owner** (`ROLE_OWNER`): everything a driver may, plus the Groups' admin, sessions and folders; not devices, agents' grants, sharing or remote access. Links are direct only, nothing is relayed through a third computer. Linked computers also forward SSH agent requests to the computer you're at (`sshagent.go`).
- Agent identity is host-local: a guest's agents reach a shared Group only through their own daemon, acting with their person's role, and never with the host's agent scope.
- **Driving is running commands as the host.** A Driver can open tabs with any command and type into shells, so a Driver (and a linked computer, which has full control both ways) can do anything the host's user can on that computer. Viewer is the only role that isn't trusted with that.
- **Planned:** a relay for peer links that can't connect directly (a blind forwarder for end-to-end encrypted links between user keys).

## 10. macOS integration

- The app bundles `hmkd` (`Contents/MacOS`), the CLI (`Contents/Helpers/hmk`), the holder and the helpers (`Contents/Helpers`), the menu bar item (`Contents/Library/LoginItems`) and the daemon's launchd plist (`Contents/Library/LaunchAgents`). Runs as you; no root.
- The app starts its daemon (in its own session) if none answers. *Run Daemon at Login* registers it with `SMAppService.agent(plistName:)`; launchd then restarts it after a crash and leaves holders running (`AbandonProcessGroup`).
- **Updates:** Sparkle (`HammockUpdates`). Release builds carry the appcast URL and EdDSA public key (`SUFeedURL`, `SUPublicEDKey`); the appcast and signed updates (with deltas) are served from hammock.jsp.scot. Checks run daily and ask before installing. After an update the app finds the running daemon is another version, asks it to exit (`ShutdownDaemon`) and starts the bundled one, which reconnects to the holders, so panes survive. Local and development builds have no feed.
- Not sandboxed (it starts and talks to helpers and private frameworks); distributed with Developer ID and notarisation, not the Mac App Store. The iPhone app is on TestFlight.
- Permissions are asked for when needed: Notifications; Screen Recording and Accessibility for screen sharing.
- **Linux:** `hmk service install` installs a systemd user service with lingering (`KillMode=process`, so holders survive restarts); `hmk start` without systemd ([linux.md](linux.md)).

## 11. Performance and benchmarking

- `just bench` runs Go benchmarks: the keystroke round trip through daemon and holder (`BenchmarkInputEcho`), output throughput (`BenchmarkOutputThroughput`), and the holder's own. Results and notes in [benchmarks.md](benchmarks.md).
- `just bench-gate` fails if the round trip's p99 exceeds 8 ms or throughput drops under 10 MB/s.
- Product budgets (+≤ 8 ms median keystroke-to-photon against Ghostty, ≥ 90% of its throughput, ≤ 1 frame added latency on visual panes) aren't yet measured end to end against Ghostty: a harness driving the app and Ghostty side by side is **planned**.
- The agent view's smoothness is measured with `scripts/agentreplay` and `HMK_DEBUG_JANK` (AGENTS.md).

## 12. Security summary

- Listeners: Unix sockets (0600, in a 0700 `run` directory) and, with remote access on, the chosen networks' addresses only (ADR-0016).
- Remote clients: TLS 1.3 pinned to the host's certificate; devices and computers sign in with keys pinned at pairing or linking; per-device revocation.
- Agents identified by process ancestry and scoped by `agentCheck` (a guardrail, not a sandbox); person-only actions are taken directly only from the app (by code signature) and otherwise ask the person, in the app or on a paired phone; audit log for agent and remote actions, pruned to 90 days / 50,000 rows.
- Pre-auth hardening: one sign-in attempt per connection, rate-limited failure audits, per-address and total caps on pending TLS and QUIC connections, source checks on both.
- Peer identity: a user id is a short hash of the user's key, so every sign-in is also checked against the full key on record.
- Push payloads end-to-end encrypted per phone; the relay sees device tokens, ciphertext, keyed-hash thread and collapse tags, and the category (join, grant, hammockkey): [relay/push/README.md](../relay/push/README.md).
- The Hammock Key signs only with your approval: Touch ID on the Mac, or a phone's Secure Enclave signature over the request, checked by the key helper itself, not the daemon.
- Untrusted content: pane output, web pages and transcripts returned to agents are labelled untrusted; links that would open something other than a web page ask first; names from other computers are stripped of control characters.
- Multiplayer: explicit invites, approval per person, roles checked by the host, stop sharing at once, visible "who is typing".

## 13. Testing

- Go unit tests per package (`just test`), including the holder's reconnect and replay, the daemon's slow-client and resync paths, agent hook parsing, and security tests for scoping, person checks and peers.
- Golden-file tests for thread codecs (`internal/thread/testdata`).
- Swift tests for the terminal engine (`cd apple && swift test`).
- Benchmarks and their gate (§11).
- UI is checked by rendering windows headlessly (`HMK_DEBUG_SNAPSHOT` and friends, AGENTS.md) and by people in a development copy.

## 14. Risks

| Risk | Mitigation |
|---|---|
| libghostty-vt API churn | Pinned commit; thin cgo wrapper; daemon and clients built from the same pin. |
| Server/client VT divergence | Same pinned engine; offset-checked streams; a fresh snapshot after any gap. |
| Private APIs (CALayerHost, CoreSimulator, SimulatorKit) break | Isolated in helpers, which check what they load; what they rely on is written down in [internals/](internals/), to recheck with each Xcode and macOS release. There is no fallback path (no `simctl io` polling). |
| Agents' hook and transcript formats change | One registry file per agent; tests per agent; screen heuristics as fallback; switching degrades to a handoff brief. |
| CEF size and complexity | One host per daemon; isolated in its helper. |
| Auto-resume runs agents unattended | Agents resume their own sessions, in their prior permission mode. |
| Scope creep | Phase gates in [build-plan.md](build-plan.md); the product spec's "Out of scope" list. |
