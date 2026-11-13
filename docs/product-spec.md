# Hammock — Product Spec

Status: describes Hammock as built (0.3), with what's still planned marked as such · Owner: Alex Stevenson-Price · Last updated: 2026-11-10

## 1. Summary

Hammock is a persistent, agent-aware workspace for software work. A background daemon on your machine owns every terminal, agent, browser, simulator, log and editor you're working with, organised into **Groups → Tabs → Panes**. Native apps (macOS, iOS), a TUI and the `hmk` CLI connect to it as views. It knows what each coding agent is doing, tells you when one needs you, and lets agents drive the workspace themselves through MCP. Your own computers can link to see each other's Groups, and you can share a live Group with someone else.

The wedge is a **native agent cockpit**: the best place on a Mac to run and supervise many coding agents at once, with no perceptible overhead compared to a standalone terminal.

## 2. Problem

People running several coding agents in parallel juggle them across terminal tabs, multiplexer sessions and windows:

- **You can't see state at a glance.** Which agent is working, which is blocked on a permission prompt, which finished ten minutes ago?
- **Attention is pull, not push.** You find out an agent needed you by checking on it.
- **Sessions are fragile.** Closing a window, upgrading a tool, or rebooting loses running work.
- **You're tethered to the desk.** Unblocking an agent requires being at the machine.
- **Agents can't use the workspace.** An agent can't open a pane to run a dev server and watch it, or open a browser to check its change, in a way you can see too.
- **Agents are siloed by vendor.** A session started in Claude Code can't continue in Codex.
- **Collaboration means screen sharing.** There's no way to hand a colleague a live, interactive view of your agents and terminals.

## 3. Audience and positioning

1. **First user: its author.** Hammock replaced their multiplexer as the daily driver. The quality bar is "I won't fall back to the old setup".
2. **Public release:** developers who run several coding agents and want a native, persistent, agent-aware workspace.
3. **Later:** small teams who collaborate on live agent work (shared Groups today; a hosted relay with accounts is a plan).

**Why not tmux / zellij / herdr / Warp / cmux?** Each does part of this. Hammock is built around having all of it together: a daemon that outlives its clients and its own upgrades; native Mac and iPhone apps; agent state for many agents, with notifications that find you on the device you're using; panes beyond terminals (browser, iOS Simulator, logs, editor) that agents can drive through MCP; and your other computers and other people's Groups in the same sidebar.

## 4. Product principles

1. **Local is transparent.** On the machine running the daemon, a Hammock terminal should feel no different from a standalone terminal like Ghostty, and a browser or simulator pane no different from a native view. This is a measured requirement (see §8), not an aspiration.
2. **The daemon is the source of truth.** Clients are views. Close every client and nothing stops.
3. **Attention is the scarce resource.** Hammock interrupts only when it's worth it, and only on the device you're using.
4. **Agents are first-class users.** Most of what you can do in the UI, an agent can do through MCP, within a scope the daemon enforces.
5. **Honest about lossy things.** Switching agents and resuming after a reboot are best-effort; the UI says what was carried over and what wasn't.
6. **Small surface, done well.** "Work OS" is deliberately limited to layout and persistence. No built-in task tracker.

## 5. Core concepts

| Concept | Definition |
|---|---|
| **Host** | A machine running the Hammock daemon (`hmkd`): a Mac, or a Linux server. Owns all processes. |
| **Group** | A named set of tabs for a piece of work. It has a folder its new tabs open in, which follows where you last worked in it. Unit of sharing and of agent scope. |
| **Tab** | A layout of panes within a Group: splits, plus panes floating over them. |
| **Pane** | A region showing one piece of content, always hosted by the daemon. Kinds: terminal, browser, simulator, log, editor. |
| **Agent** | A coding-agent process (Claude Code, Codex and others: [agents.md](agents.md)) running in a terminal pane, with tracked state. |
| **Thread** | A vendor-neutral record of an agent conversation, independent of the pane and agent that produced it. Used to switch agents. |
| **Client** | A frontend connected to a host: the Mac app, the iPhone app, the TUI, `hmk`. |
| **Linked computer** | Another of your own hosts, whose Groups you see and work in as their owner. |
| **Scheduled task** | An agent's prompt or a command the daemon runs on a schedule, each run in a new tab. |

### Working directory behaviour

New panes and tabs in a Group open in the Group's folder: the working directory of its last focused terminal, kept by the daemon as you work, so a Group whose tabs have all closed still opens new ones there. You can also set it (Change Folder…, `hmk group folder`), or create a Group from a folder (File ▸ New Group in Directory…, `hmk group new --folder`).

## 6. Features

### 6.1 Terminal multiplexing

- Create, rename, reorder and close Groups, tabs and panes. Split in four directions, resize, zoom a pane, swap panes, merge a tab into another and break a pane out again. Panes can float over a tab, dock back into its splits or be minimised.
- Terminals parsed by libghostty-vt in the daemon and in each client, and drawn natively: true colour, the Kitty keyboard protocol, Kitty graphics, ligatures, synchronised output, OSC 8 hyperlinks, OSC 7 working-directory reporting.
- Scrollback that survives client disconnects and daemon upgrades.
- Several clients on one pane at once. The PTY takes the size of whoever typed into or focused it last; the others fit it into their space.
- Reopen the last closed tab or pane (agents resume their sessions).
- Not built yet: searching a terminal's scrollback, and a keyboard copy mode.

### 6.2 Agent cockpit

- **Agent detection** for every agent in the registry ([agents.md](agents.md)): through its hooks where Hammock can install them (`hmk hooks install`), otherwise from its process under the pane's shell and what its screen shows.
- **Agent states:** `starting`, `working`, `needs_input` (a permission prompt or question), `idle` (finished a turn), `errored`, `exited`.
- **Sidebar:** each tab shows its agent's state, and how long it's been working; collapsed Groups show a summary.
- **Needs You:** an attention queue pinned to the bottom of the sidebar: agents waiting for input, errors, permission requests from agents using Hammock, people asking to join a shared Group and computers asking to link, with Finished (turns done) below. Acting on an item focuses its pane.
- **Status everywhere:** state on Group, tab and pane chrome, the Dock badge, and the menu bar item (which also shows the server's state and who's connected).
- **Agent view:** an agent pane can show its conversation natively instead of the terminal: messages, tool calls and diffs, subagents, the task list, pull requests and artifacts the session made, and cards to answer questions and permission prompts. On the Mac and the iPhone.
- **Session finder:** every agent's sessions on this computer or a linked one, searchable, ready to resume (⌘⇧O, `hmk sessions`, `hmk resume`).
- **Usage limits:** agents' subscription limits in the sidebar, read from disk or, if you turn it on, asked of each agent's own service.

### 6.3 Notifications

Triggers, each one a setting: an agent **needs input**, asks **permission**, **finished a turn**, or **errored**.

Presence-aware routing:

1. If you're looking at that pane, no notification (unless you turn that on).
2. On the Mac: in the window while it's in front, otherwise as a macOS notification (or always one or the other: `notify-style`).
3. A push to your iPhone only while you're away from your Macs: the screen locked, untouched for a couple of minutes, or Hammock used on the phone since. It waits until then, and is dropped if the agent is answered first.
4. Always added to Needs You.

Tapping a notification opens the pane. Permission requests, join requests and Hammock Key approvals can be answered from the notification itself.

Not built: a notification when a long-running command that isn't an agent finishes, and muting a Group or an agent.

### 6.4 Persistence and restore

- Processes survive client disconnects, the Mac app quitting, and **daemon upgrades and crashes**: each PTY lives in its own holder process.
- After a **reboot or logout**, Hammock restores every Group, tab and pane with its folder and a scrollback snapshot, starts the shells again, and **resumes each agent's session** (`claude --resume`, `codex resume` and the like). Browser pages come back at their URL.
- **Sleeping agents** (off by default; Settings, or `agent-sleep = 3h` … `48h` in the config file): an agent idle that long, with nothing running and not waiting for you, has its process ended to free memory. Its pane keeps its screen, dimmed under a big **Wake** button; that button (on any device), typing in it or messaging its agent resumes the session where it was, with the options it was started with. Only clicking into it doesn't, as that's easy to do by accident. **Put Agent to Sleep** (and `hmk sleep`) does it by hand. Sleeping agents stay asleep through a restart.

### 6.5 Templates (not built)

The plan was a TOML file describing a Group's tabs, splits and commands (`hmk group new --template`, "Save Group as template"). It hasn't been built. The closest thing is a Group created from a folder (§5).

### 6.6 MCP: agents driving the workspace

`hmk mcp` is an MCP server (`hmk hooks install` registers it with the agents that take one). Agents can:

- **Panes:** list Groups, tabs and panes; read a pane's screen and scrollback; open tabs and splits with a command; type into a pane; wait for output matching a pattern or for the process to exit; arrange and close panes; show a file or page beside themselves.
- **Browser panes:** open, navigate, read the page as an outline, click, type, press keys, evaluate, screenshot, wait for text, and read the console and network.
- **Simulator panes:** list devices, open one, screenshot, tap, swipe, type, press buttons, rotate, pinch, fold, install, launch and terminate apps, open URLs, read logs, shut down.
- **Log and editor panes:** open and read them filtered; open a file with lines selected, and read what you have open.
- **Scheduled tasks and sessions:** list, save, run, pause and delete schedules; search and read past agent sessions.

**Scope:** the daemon works out who's asking from the connecting process, so it can't be talked out of it. An agent may read the panes in **its own Group** and fully control the panes it opened. Typing into, closing or scripting other panes, or anything outside its Group, asks you first (once, or always for that Group). DevTools can do anything in its page, so an agent opening or using DevTools for a page it didn't open asks to control that page, even in a DevTools pane it opened itself. Pairing, sharing, linking and the like are yours alone. The scope is a guardrail against an agent wandering, not a sandbox: for a real boundary, use the agent harness's own. Agents' actions are in the audit log (Settings ▸ Devices ▸ Recent Activity, `hmk audit`).

### 6.7 iPhone app

- Cockpit: Groups, agents and their states, Needs You.
- Push notifications, end-to-end encrypted, with deep links and answers in the notification (§6.3).
- Full interactive terminal (libghostty-vt) with a keyboard accessory bar (Esc, Ctrl, Tab, arrows).
- The agent view, editor panes, scheduled tasks, and approving the Hammock Key.
- Browser and simulator panes as live video, driven by taps, swipes and typing.
- The Mac's whole screen, when screen sharing is on.
- Connects over Tailscale, your local network or a VPN (the networks you choose); paired by a QR code from the Mac or `hmk pair`.
- In beta on TestFlight.

### 6.8 TUI

- `hmk tui`: a sidebar of Groups and tabs, tabs, splits and floating panes, composited into the host terminal, with mouse support and a Ctrl-B prefix. Good over SSH, and the only UI on Linux.
- Browser and simulator panes drawn with Kitty graphics where the terminal supports them (Ghostty, Kitty); placeholders elsewhere. Editor panes, or your `$EDITOR`.

### 6.9 Rich panes

1. **Browser:** a real Chromium (CEF) in a helper the daemon starts, one per daemon, sharing one profile. On the Mac it's composited from Chromium's own GPU layer with native input, so it feels like a native view, and it keeps running when the app quits. Find in page, DevTools as a pane, and agent control over CDP.
2. **iOS Simulator:** a simulator's screen in a pane at native fidelity, with touch, typing, hardware buttons, rotation, pinch and foldables; agents can drive it too.
3. **Log viewer:** a file, the Mac's `log stream`, a simulator's log, `adb logcat` or any command, parsed into entries with levels, filtering and search.
4. **Editor:** a text file in a pane (CodeMirror), its unsaved text kept by the daemon and shared live by every client; syntax highlighting, find and replace, a git change gutter and optional Vim keys.
5. Maybe later: an Android emulator.

### 6.10 Agent switching

- "Continue in Codex" / "Continue in Claude" on an agent pane, or `hmk switch`.
- Hammock stops the current agent, converts the Thread into the target agent's native session format, and resumes it in the same pane and folder.
- If conversion fails validation, Hammock falls back to a generated **handoff brief** (goal, progress, decisions, open todos, files changed) seeded into a fresh session.
- The UI says what was carried over and what was dropped.
- Claude Code ⇄ Codex only for now: other agents have no thread codec yet.
- Threads are stored locally; the data model leaves room for end-to-end encrypted sync between your own hosts later.

Prior art: Atuin Threads (neutral record store, native-format rewrite on resume).

### 6.11 Shared Groups

- Share a Group with another Hammock user through an invite link; it appears in their sidebar among their own Groups, with the host's avatar and computer name.
- All processes run on the **host's** machine.
- Roles: **View** (watch) and **Drive** (type, answer agents, open and close tabs, drop files). The host approves each person once, changes roles and removes people, and can stop all sharing at once.
- Presence: avatars on the tabs and panes each person is looking at, and "is typing" while they type.
- Only the panes a guest is looking at are streamed: terminals as bytes, browser and simulator panes as video.
- Guests reach the host over Tailscale, or, once other networks are chosen for remote access (Settings ▸ Devices), over those (a local network or a VPN). Every remote action is in the audit log with the person who did it. A guest's agents act with their person's role.
- Planned: push notifications for guests, a Follow mode, Groups spanning several hosts, and a hosted end-to-end encrypted relay with accounts and SSO (the paid tier).

### 6.12 Linked computers

- Link your own computers (Macs and Linux servers) with a six-digit code checked on both. Each then shows the others' Groups in its sidebar, and you work in them as their owner: open tabs, start agents, find and resume sessions there.
- Panes on a linked computer can use the SSH keys of the one you're at.

### 6.13 Scheduled tasks

The daemon runs an agent's prompt or a command on a schedule (times and days, or cron), each run in a new tab in its Group, whether or not an app is open. On the Mac, the iPhone, the TUI, `hmk schedule` and MCP; an agent adding or changing one asks you first.

### 6.14 The Hammock Key and SSH

An SSH key kept in the Mac's Secure Enclave that signs only when you approve, with Touch ID at the Mac or from your iPhone when you're away. Panes get Hammock's own SSH agent, which passes other requests on to yours (1Password, Secretive and the like), so the key works for ssh, git and jj signing alongside your usual keys.

### 6.15 Screen sharing

Optional: your iPhone or another linked Mac can see and control this Mac's whole screen, at the lock screen too, for the things only the GUI does.

## 7. Platforms and distribution

- **Host:** macOS 26 or later, and Linux (glibc 2.28 or later; amd64 and arm64), with a plugin for Unraid ([linux.md](linux.md)). No app on Linux: `hmk` and the TUI. Windows isn't planned.
- **Clients:** the Mac app, the iPhone app (iOS 26), the TUI and `hmk`.
- **Install:** one download of Hammock.app, which updates itself (Sparkle). Onboarding registers the bundled daemon as a login item (no admin), installs the `hmk` command, and installs hooks for the agents it finds (showing the changes first, keeping a backup). The iPhone app is on TestFlight. Homebrew and apt packages are planned.
- **Licence:** the whole repository is open source under the MIT licence. The Hammock name and icon aren't covered by it.

## 8. Non-functional requirements

| Area | Requirement |
|---|---|
| Terminal latency (local) | Keystroke-to-photon within **8 ms (≈1 frame at 120 Hz)** of standalone Ghostty on the same machine. The daemon and holder's share is measured ([benchmarks.md](benchmarks.md)); the end-to-end comparison isn't built yet. |
| Terminal throughput (local) | **≥ 90%** of standalone Ghostty throughput (e.g. `cat` of a large file), measured against the PTY's own ceiling for now. |
| Visual panes (local) | **≤ 1 frame** added latency; no compression artefacts. |
| Reliability | No process loss on client quit, daemon crash or daemon upgrade. |
| Resource use | Idle daemon < 1% CPU; per-pane overhead < 5 MB RSS excluding scrollback. |
| Security | No network listener but the local socket and, with remote access on, the networks you choose; per-device and per-computer keys; an audit log for remote and agent actions. |
| Privacy | No telemetry. Push payloads are encrypted end to end; the push relay never sees content. |

## 9. Out of scope (for now)

- A built-in task or issue tracker, or deep Linear/GitHub integration.
- Git-aware Groups (worktree management, PR status on Groups).
- Windows hosts.
- Cursor- or selection-level presence inside panes.
- Cloud-hosted execution.

## 10. Success metrics

- Its author uses Hammock exclusively, day to day.
- Median time from "agent needs input" to the user responding drops versus a plain-multiplexer baseline.
- Zero lost sessions across daemon upgrades.
- Latency and throughput benchmarks stay inside budget on every main-branch build (`just bench-gate`).

## 11. Open questions

- Pricing and packaging of the paid tier (the hosted relay, accounts, SSO).
- The iPhone keyboard for heavy terminal use.
- Browser profiles: one profile per host today; whether Groups should get their own.
