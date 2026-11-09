# Hammock — Product Spec

Status: Draft v0.1 · Owner: Alex Stevenson-Price · Last updated: 2026-10-29

## 1. Summary

Hammock is a persistent, agent-aware workspace for software work. A background daemon on your machine owns every terminal, agent, browser, and simulator you're working with, organised into **Groups → Tabs → Panes**. Native apps (macOS, iOS) and a TUI connect to it as views. It knows what every coding agent is doing, tells you when one needs you, lets agents drive the workspace themselves via MCP, and — later — lets you share a live Group with a collaborator over the internet.

The wedge is a **native agent cockpit**: the best place on a Mac to run and supervise many coding agents at once, with zero perceptible overhead compared to a standalone terminal.

## 2. Problem

People running several coding agents in parallel juggle them across terminal tabs, tmux/herdr sessions, and windows:

- **You can't see state at a glance.** Which agent is working, which is blocked on a permission prompt, which finished ten minutes ago?
- **Attention is pull, not push.** You find out an agent needed you by checking on it.
- **Sessions are fragile.** Closing a window, upgrading a tool, or rebooting loses running work.
- **You're tethered to the desk.** Unblocking an agent requires being at the machine.
- **Agents can't use the workspace.** An agent can't open a pane to run a dev server and watch it, or launch a browser to verify its change, in a way you can see too.
- **Agents are siloed by vendor.** A session started in Claude Code can't continue in Codex.
- **Collaboration means screen sharing.** There's no way to hand a colleague a live, interactive view of your agents and terminals.

## 3. Audience and positioning

1. **Phase 1 user: its author.** Hammock replaces their multiplexer as the daily driver. Quality bar is "I won't fall back to the old setup".
2. **Public release:** developers who run multiple coding agents and want a native, persistent, agent-aware workspace.
3. **Later:** small teams who collaborate on live agent work (multiplayer, then a hosted relay with SSO).

**Why not tmux / zellij / herdr / Warp / cmux?** Hammock is the only one combining: a persistent daemon that outlives clients and upgrades; native macOS and iOS clients with Ghostty-grade rendering; first-class agent state and push notifications; panes beyond terminals (browser, simulator) that agents can drive; and multiplayer Groups.

## 4. Product principles

1. **Local is transparent.** On the machine running the daemon, a Hammock terminal must be indistinguishable from standalone Ghostty, and a browser/simulator pane indistinguishable from a native view. This is a measured requirement (see §8), not an aspiration.
2. **The daemon is the source of truth.** Clients are views. Close every client and nothing stops.
3. **Attention is the scarce resource.** Hammock interrupts only when it's worth it, and only on the device you're using.
4. **Agents are first-class users.** Anything you can do in the UI, an agent can do via MCP — within the permissions you grant.
5. **Honest about lossy things.** Switching agents and resuming after reboot are best-effort; the UI says what was preserved and what wasn't.
6. **Small surface, done well.** "Work OS" is deliberately limited to layout and persistence. No built-in task tracker.

## 5. Core concepts

| Concept | Definition |
|---|---|
| **Host** | A machine running the Hammock daemon. Owns all processes. |
| **Group** | A named group of tabs representing a piece of work. Has no stored root directory. Can be created from a template. Unit of sharing and of agent permission scope. |
| **Tab** | A layout of panes within a Group (splits). |
| **Pane** | A region showing one piece of content, always hosted on the server. Kinds: Terminal (v1), Browser, Simulator, Log viewer, later File viewer/editor and Android emulator. |
| **Agent** | A coding-agent process (Claude Code, Codex, …) running in a terminal pane, with tracked state. |
| **Thread** | A vendor-neutral record of an agent conversation, independent of the pane and agent that produced it. Enables resume and switching. |
| **Client** | A frontend connected to a host: macOS app, iOS app, TUI. |
| **Template** | A declarative TOML file describing a Group's tabs, panes, and commands. |

### Working directory behaviour

New panes and tabs in a Group default to the current working directory of the **last focused pane** in that Group. A Group therefore tends to cluster around one repo, but the directory is not stored on the Group or set by the user.

## 6. Features

### 6.1 Terminal multiplexing (v1)

- Create, rename, reorder, and close Groups, tabs, and panes. Split horizontally and vertically; resize; zoom a pane.
- Full-fidelity terminals powered by libghostty: true colour, Kitty keyboard protocol, Kitty graphics, ligatures, synchronized output, OSC 8 hyperlinks, OSC 7 cwd reporting.
- Scrollback that survives client disconnects and daemon upgrades.
- Multiple clients attached to the same pane simultaneously. The PTY takes the size of the **active driver** (the client that most recently typed into or focused it); other clients letterbox/scale.
- Search scrollback; copy mode; select across scrollback.

### 6.2 Agent cockpit (v1 — the wedge)

- **Agent detection** for Claude Code and Codex via their hook systems; heuristic detection (process tree, title, OSC, screen patterns) for anything else.
- **Agent states:** `starting`, `working`, `needs_input` (permission prompt or question), `idle` (finished a turn), `errored`, `exited`.
- **Cockpit sidebar:** all agents across all Groups with state, Group/tab, elapsed time, last message summary. Sort by "needs me first".
- **Attention queue:** an ordered inbox of agents needing input, finished turns, and errors. Clearing an item focuses the pane.
- **Status everywhere:** state badges on Group, tab, and pane chrome; aggregate counts in the menu bar and Dock icon.
- **Quick actions:** approve/deny permission prompts, send a short reply, interrupt (Esc / Ctrl-C), restart, resume.

### 6.3 Notifications (v1)

Triggers on by default: **agent needs input**, **agent finished a turn**, **agent errored / process exited non-zero**. Opt-in: long-running non-agent command finished (> configurable threshold).

Presence-aware routing:

1. If you're currently viewing that pane on any client → no notification.
2. Otherwise deliver to the **most recently active device**: a Mac banner if the Mac app saw input in the last ~2 minutes; else an iOS push.
3. Always add to the attention queue.
4. Per-Group and per-agent mute; Do Not Disturb respects OS Focus.

Tapping a notification deep-links to the pane.

### 6.4 Persistence and restore (v1)

- Processes survive client disconnects, Mac app quit, and **daemon upgrades/crashes** (each PTY lives in its own holder process).
- On **reboot/logout**, Hammock restores every Group, tab, and pane with its cwd and a scrollback snapshot, restarts shells, and **automatically resumes agent sessions** (`claude --resume`, `codex resume`). A banner on each resumed pane states it was auto-resumed and from which session.
- A global setting can disable auto-resume.

### 6.5 Templates (v1)

- `*.hammock.toml` files declare tabs, splits, and per-pane commands/cwd/env.
- "Save Group as template" exports the current layout.
- Create a Group from a template via the app, CLI (`hmk group new --template web-dev.toml`), or MCP.
- Live state is stored separately in the daemon's database; templates are inputs, not live state.

### 6.6 MCP: agents driving the workspace (v1, read + terminal; later all pane kinds)

The daemon exposes an MCP server. Each agent launched in Hammock receives a scoped capability token automatically.

Capabilities (tools):

- Inspect: list Groups/tabs/panes, read pane screen and scrollback, get agent states.
- Create: new tab/pane with a command, from a template.
- Interact: send input to a pane, wait for output matching a pattern / process exit, resize, close.
- Later: drive browser panes (navigate, click, evaluate, screenshot, DevTools data), drive simulators (launch app, tap, swipe, screenshot, logs).

**Permissions:** by default an agent may fully control panes it created and read other panes in **its own Group**. Writing to panes it didn't create, or any access outside its Group, requires a grant (prompted in the app, rememberable). Users can configure defaults globally, per Group, and per agent. Every MCP action is written to an audit log visible in the app.

### 6.7 iOS app (v1, after the Mac app)

- Cockpit: agent list, attention queue, state badges.
- Push notifications with deep links; **actionable notifications** (Approve / Deny / Reply) for permission prompts.
- Full interactive terminal (libghostty) with a keyboard accessory bar (Esc, Ctrl, Tab, arrows, common chords).
- Browser and simulator panes **view-only**.
- Connects over Tailscale; paired by QR code from the Mac app.

### 6.8 TUI (v1)

- A thin TUI is the first client: the protocol harness and an SSH-friendly daily driver.
- Full multiplexer UX (sidebar of Groups/agents, tabs, splits) composited into the host terminal.
- Visual panes rendered via Kitty graphics where supported; placeholders elsewhere.

### 6.9 Rich panes (post-v1, in order)

1. **Browser:** a real Chromium (CEF) owned by the daemon. Locally composited in the Mac app with native input — feels like a native view and keeps running when the app quits. Agents control it at CDP level. DevTools available.
2. **iOS Simulator:** a simulator's display shown in a pane at native fidelity locally, with touch/keyboard input; agents can launch, tap, screenshot, and read logs.
3. **Log viewer:** structured tailing with filtering, levels, and search over files, `log stream`, `adb logcat`, simulator logs.
4. Later: file viewer/editor, Android emulator.

### 6.10 Agent switching (post-v1)

- "Continue in Codex" / "Continue in Claude" on any agent pane.
- Hammock stops the current agent, converts the Thread into the target agent's native session format, and resumes it in the same pane and cwd.
- If conversion fails validation, Hammock falls back to a generated **handoff brief** (goal, progress, decisions, open todos, files changed) seeded into a fresh session.
- The UI states what was carried over (messages, tool calls, diffs) and what was dropped (reasoning, images, oversized tool output).
- Threads are stored locally first; the data model is designed for future E2EE sync between your own hosts.

Prior art: Atuin Threads (neutral record store, native-format rewrite on resume).

### 6.11 Multiplayer Groups (later)

- Share a Group with another Hammock user; it appears in their app as a **remote Group** (badged with the host's name).
- All processes run on the **host's** machine. The guest cannot spawn processes on their own machine inside a shared Group.
- Roles: **Viewer** (watch), **Driver** (type, create panes), **Admin** (manage sharing). The host has a one-click kill switch that disconnects everyone.
- Presence: avatars with coloured rings on the Group, tab, and pane each collaborator is viewing; a glow while they type; a "Follow" mode to mirror someone's view.
- Bandwidth: only the panes a guest is actually viewing are streamed; terminals as byte streams, visual panes as adaptive video.
- Connection: direct/Tailscale first. Later: hosted E2EE relay and SSO (the commercial offering).
- Every remote action is audit-logged with the actor's identity. An agent's scope never extends to another user's Groups.

## 7. Platforms and distribution

- **Host:** macOS first (latest two releases). Code kept portable; Linux builds in CI, shipped later. Windows later still.
- **Clients:** TUI → macOS app → iOS app.
- **Install:** one download of Hammock.app. Onboarding registers the bundled daemon as a login item (user-level LaunchAgent, no admin), installs agent hooks for Claude Code / Codex (with consent), optionally symlinks the `hmk` CLI, and offers iOS pairing. Homebrew formula for headless/TUI-only installs.
- **Licence:** open core. Daemon, protocol, CLI, and TUI are open source; native apps and hosted relay/SSO are commercial.

## 8. Non-functional requirements

| Area | Requirement |
|---|---|
| Terminal latency (local) | Keystroke-to-photon within **8 ms (≈1 frame at 120 Hz)** of standalone Ghostty on the same machine. Enforced by benchmark in CI. |
| Terminal throughput (local) | **≥ 90%** of standalone Ghostty throughput (e.g. `cat` of a large file). |
| Visual panes (local) | **≤ 1 frame** added latency; no compression artefacts. |
| Reliability | No process loss on client quit, daemon crash, or daemon upgrade. |
| Resource use | Idle daemon < 1% CPU; per-pane overhead < 5 MB RSS excluding scrollback. |
| Security | No network listener except localhost and the Tailscale interface; per-device keys; audit log for remote and MCP actions. |
| Privacy | No telemetry without opt-in. Push payloads are encrypted end-to-end; the push relay never sees content. |

## 9. Out of scope (for now)

- A built-in task/issue tracker or deep Linear/GitHub integration.
- Git-aware Groups (worktree management, PR status).
- Windows hosts; Linux shipped binaries before public release.
- Mixed-host shared Groups (panes from several machines in one Group).
- Cursor/selection-level presence inside panes.
- Cloud-hosted execution.

## 10. Success metrics

- **Milestone 1:** its author uses Hammock exclusively for 7 consecutive days.
- Median time from "agent needs input" to the user responding drops versus a plain-multiplexer baseline.
- Zero lost sessions across daemon upgrades during dogfooding.
- Latency/throughput benchmarks stay inside budget on every main-branch build.

## 11. Open questions

- Pricing and packaging of the commercial tier.
- Exact iOS keyboard UX for heavy terminal use.
- Whether templates should be auto-detected from repos (`.hammock/group.toml`) before public release.
- Browser profiles: one shared dev profile per host vs per-Group profiles.
