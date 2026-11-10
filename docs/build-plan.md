# Hammock — Phased Build Plan

Status: Phases 0–10 built and in daily use; Phase 11 (public release) under way · Owner: Alex Stevenson-Price · Last updated: 2026-11-10
See [product-spec.md](product-spec.md), [tech-spec.md](tech-spec.md) and the decisions in [adr/](adr/).

How Hammock was built, and what's next. The phases were an ordered backlog, not gates: work moved between them as it was useful, and some of what shipped was never in a phase (see [Built beyond the phases](#built-beyond-the-phases)). Each phase is kept as it was planned, with notes where what was built differs. A date is when a phase was finished.

Open work, follow-ups and known gaps are [GitHub issues](https://github.com/homegrownsw/hammock/issues), not this page: where a phase left something undone, it links the issue.

## Phase 0 — Foundations and spikes  ✅ done (2026-10-30)

Retire the riskiest technical unknowns before building on them.

- Monorepo scaffold (§2 of tech spec), CI, Xcode workspace skeleton. As built, CI runs on macOS (Go vet and tests, the benchmark gate, a Swift build: `.github/workflows/ci.yml`); the Linux builds are cross-compiled at release (Phase 11).
- **Spike: libghostty-vt via cgo.** Build the static lib with Zig from a pinned commit; Go wrapper that feeds bytes, reads the screen, and serialises a snapshot that a second libghostty instance can replay to an identical screen.
- **Spike: pane-holder.** PTY + ring buffer + Unix socket; kill/restart a parent repeatedly under `yes`-style load with zero byte loss.
- **Spike: local latency path.** Minimal Swift app embedding libghostty, fed through UDS → Go → holder. Measured: the keystroke round trip through daemon and holder is about 0.05 ms ([benchmarks.md](benchmarks.md)). Keystroke-to-photon against Ghostty itself isn't built yet: [#19](https://github.com/homegrownsw/hammock/issues/19).
- Benchmark harness v0 (latency and throughput), as Go benchmarks in the packages they measure (`just bench`), not a separate `bench/` directory.
- `proto/` v0: Hello, Subscribe, Group/Tab/Pane CRUD, Attach, Input, Output, Resize.

## Phase 1 — Daemon + thin TUI (terminal multiplexer core)  ✅ done (except templates)

- `hmkd`: SQLite state store, event bus, protocol server over UDS, holder supervision, VT mirrors, snapshot + stream + resync, active-driver sizing.
- Groups / tabs / panes / splits; cwd inheritance from the last focused pane.
- Daemon restart/upgrade handover with holder reconnection.
- Reboot restore: layout, cwd, scrollback snapshot, shells respawned.
- `hmk` CLI (scriptable control) and a thin TUI: sidebar, tabs, splits, compositor over per-pane libghostty-vt.
- Templates (a TOML file describing a Group, `hmk group new --template`, "save as template"): **not built.** The closest thing shipped is File ▸ New Group in Directory… (`hmk group new` with a folder).

## Phase 2 — Agent awareness + MCP v1  ✅ done

- Harness adapters for Claude Code and Codex: detection, hook installation (`hmk hooks install`), launch/resume commands. Since grown to every agent in one registry ([ADR-0015](adr/0015-agent-registry.md), [agents.md](agents.md)).
- Agent state machine; heuristic fallback detection on VT mirrors.
- Attention queue (daemon model + TUI view), shown as Needs You.
- Auto-resume of agents after reboot. (Built without the planned banners: agents simply resume as their panes come back.)
- MCP server v1 (terminal tools) with the group-scoped default policy, grant requests, and the audit log. (Built without capability tokens: the daemon identifies an agent from the connecting process instead, tech spec §8.)
- Presence reporting from clients (groundwork for notification routing).

## Phase 3 — macOS app (the wedge)  ✅ done

- HammockKit (Swift client SDK: protocol, models, libghostty view wrapper).
- Mac app: window chrome, Group/tab/pane UI, splits, cockpit sidebar, attention queue, quick actions (approve/deny/reply/interrupt), menu bar + Dock badges.
- Mac notifications with presence-aware routing.
- Onboarding: SMAppService daemon registration, hook installation with diff preview, CLI symlink (Settings ▸ General installs `hmk`).
- Grant-request prompts and audit log viewer (Settings ▸ Devices ▸ Recent Activity, `hmk audit`).
- Bench gates in CI on Mac runners (`just bench-gate`, `.github/workflows/ci.yml`). The workflow is written; GitHub Actions are switched on once the repo is public ([#38](https://github.com/homegrownsw/hammock/issues/38)).
- Daemon self-update via the app: it replaces a daemon whose version differs from the bundled one.
- **⌘K command palette**: a Spotlight/Raycast-style overlay above the whole window to jump between sessions (Groups, tabs, panes, agents) and run any Hammock command or action. Fuzzy search, keyboard-first, and playful: a spring on open, an icon bounce and a row flash on confirm. (The sparkle bursts first planned were dropped.)
- **Group icons** (Configure Group…): an emoji or a picture as a Group's icon, in place of the stack glyph; stored with the Group, so it persists and shows on every client.
- **Groups named after their directory**: an unnamed Group shows the name of its current directory (the cwd of its last-focused pane, e.g. "Hammock"), following it as it changes, until it's renamed by hand.

## Phase 4 — iOS app + remote access  ✅ done

- ✅ Tailscale-interface listener, TLS, device pairing (QR), per-device keys and revocation. Since 2026-11-08 remote access also works on a local network or a VPN ([ADR-0016](adr/0016-remote-access-on-any-network.md)).
- ✅ Push relay (Cloudflare Worker, `relay/push`), E2EE payloads, Notification Service Extension, actionable notifications. Follow-ups: [#14](https://github.com/homegrownsw/hammock/issues/14), [#45](https://github.com/homegrownsw/hammock/issues/45), [#48](https://github.com/homegrownsw/hammock/issues/48), [#51](https://github.com/homegrownsw/hammock/issues/51).
- ✅ iOS app: cockpit, attention queue, full terminal with accessory bar, deep links.
- ✅ Notification routing across Mac + iOS: pushes only while you're away from the Mac (locked, idle 2 minutes, or using Hammock on the phone since), held until then and dropped if dealt with first.
- ✅ zstd compression of remote links (the phone's and guest daemons'): one stream per connection, flushed per message, so terminal output, agent view and state updates all reuse what came before (`internal/wirezstd`, negotiated as the `hammock.v1+zstd` WebSocket subprotocol; video frames go uncompressed).

## Phase 5 — Threads and agent switching  ✅ done (2026-11-01)

- ✅ Thread store (append-only, content-hashed, sync-ready schema) ([ADR-0006](adr/0006-own-neutral-thread-store.md)).
- ✅ Transcript codecs for Claude Code and Codex (Codex's compressed rollouts too, and its 0.16x `exec` scripts read as commands and edits), with golden-file tests per harness version (`internal/thread/testdata/golden`: hand-made fixtures in each version's shapes; add one when a version changes its format).
- ✅ Switch flow: stop, encode, resume in the same pane; handoff-brief fallback. Tried both ways with the real CLIs.
- ✅ UI: "Continue in Codex/Claude", a carry-over report (a toast, `hmk switch`'s output), and the session finder: ⌘⇧O on the Mac, ⌃B O in the TUI, `hmk sessions` / `hmk resume`, over an index of every agent session on the Mac (`internal/sessions`). Later also on the iPhone, and for agents as the MCP tools `session_search` and `session_read`.

## Phase 6 — Browser panes  ✅ done

- One browser host per daemon ("Hammock Browser", CEF): pages keep running across app quits and daemon restarts, and come back at their URL after a reboot ([ADR-0011](adr/0011-browser-panes-hidden-window-host.md), [notes](internals/browser-panes.md)).
- Mac pane: Chromium's GPU layer via CALayerHost, raw input forwarding, address bar, back/forward/reload, popups as tabs, `<select>`/context menus/JS dialogs/file pickers shown by the app, downloads to ~/Downloads, favicons and page titles in the sidebar.
- `hmk browser URL`, `hmk nav PANE …`; placeholders in the TUI and on iOS.
- Left for later phases, and built in Phase 9: find in page (⌘F), MCP browser tools and DevTools, remote/iOS viewing and TUI graphics.

## Phase 7 — Agent view  ✅ done on Mac and iOS (2026-10-30)

When Claude Code runs in a pane, toggle between the raw terminal and a rich conversation view, as in Claude's desktop and mobile apps. Especially for iOS, where reading and answering an agent beats driving a TUI on a phone keyboard. Planned for Claude only, with Codex later; as built, the agent view now reads most agents Hammock knows ([agents.md](agents.md)). The TUI keeps the raw terminal.

**Data**
- Turns come from Claude Code's session file (JSONL; the hooks already report its path). The screen supplies only live state the file doesn't have yet: working, the prompt on screen.
- The daemon tails and decodes the file (building on `internal/thread`), and streams typed blocks with ids and deltas to every client: text, thinking, tool call + result, edit/diff, image, question, permission, plan, subagent. Clients render natively.
- History: the last ~40 turns, with older ones paged in on scroll.
- Files: the daemon serves a file to clients only if the session referenced it (read, written or linked), with size caps. Images render inline everywhere; paths open in the editor/Finder on the Mac and as a preview on iOS.

**Interaction**
- Native UI for Claude's question tool (options menu), permission prompts (Allow/Deny), plan approval, and interrupt.
- Answers are typed into the pane as the keystrokes the CLI expects, only after the screen shows the expected prompt. If it doesn't, nothing is sent; the card offers "Answer in terminal" (flips the pane) and the mismatch is logged.
- Composer: send while the agent works (queued, shown as pending), slash-command completion, image attachments (saved to a file and referenced), and dictation. Follow-ups: [#12](https://github.com/homegrownsw/hammock/issues/12), [#13](https://github.com/homegrownsw/hammock/issues/13).

**Presentation**
- Native SwiftUI on Mac and iOS: a lazy list of block views; swift-markdown to AttributedString; a small code highlighter.
- Tool calls as compact rows ("Ran `go test ./...` ✓", "Edited AppModel.swift +12 −3") that expand to output or diff; consecutive calls group ("Used 6 tools"); thinking collapsed ("Thought for 8s").
- Subagents as a card with live status and the result, opening the subagent's own transcript in a sheet.
- Fonts: separate prose (default the system font) and code (default the terminal font) settings with size and line spacing, per device; iOS follows Dynamic Type.
- The toggle switches the pane in place (header control + shortcut); split the pane for side-by-side. Default view is a per-device setting (Mac: terminal, iOS: agent view), remembered per pane once toggled. A notification opens the agent view at the waiting question.

**Order (riskiest first)**
1. Spike keystroke answering (question options, permissions, plan approval) against the real Claude CLI, with screen checks ([notes](internals/agent-view-keys.md)).
2. The daemon's turn stream and file serving.
3. The Mac view: read-only, then interactive.
4. The iOS view.
5. Composer extras: slash completion, images, dictation.

## Phase 8 — Multiplayer Groups  ✅ done (built 2026-10-30; checked between two Macs)

Decided 2026-10-30 (see [ADR-0008](adr/0008-federated-host-owned-multiplayer.md)):

- **Reach:** Tailscale for now, nothing depending on it: an invite link lists endpoints, so a hosted relay can be added later as a blind pipe. TLS end to end with the host's pinned certificate; every connection signed with the guest Mac's key. (As built, guests come over Tailscale until other networks are chosen for remote access, then over those: [ADR-0016](adr/0016-remote-access-on-any-network.md).)
- **Identity:** one key per Mac; shown as the macOS account picture (coloured initials without one), the person's name and the Mac's name. Settings ▸ Sharing to change them.
- **Inviting:** one reusable link per Group until revoked or regenerated. The host approves each person once per Group, choosing View or Drive; they then reconnect on their own until removed. Approvals: Mac notification + Needs You, and the phone (Needs You, and a notification with Let View / Decline).
- **Roles:** View (watch) and Drive (type, answer agents, open/split/close/rename tabs, drop files, which upload to the host, 20 MB cap). Only the host invites and changes roles. Remove a person, stop sharing a Group, or Stop All Sharing (kill switch).
- **Guests' sidebar:** shared Groups mix in with their own, reorderable, each with the host's avatar and Mac name; dimmed with their last state while the host is unreachable.
- **Together:** presence avatars on tabs, panes and collapsed Groups, a typing glow; a pane takes the size of whoever typed last; agents needing input notify the host and Drivers, and whoever answers clears it for all.
- **Agents:** a guest's agents act in a shared Group with their person's role (Drive means Drive), within the usual agent scope.
- **Later:** push for guests ([#2](https://github.com/homegrownsw/hammock/issues/2)), follow mode ([#3](https://github.com/homegrownsw/hammock/issues/3)), Groups spanning Macs ([#4](https://github.com/homegrownsw/hammock/issues/4)), phone presence ([#5](https://github.com/homegrownsw/hammock/issues/5)); the relay is Phase 12. (Browser panes in shared Groups came with remote video, Phase 9.)

Build order: the daemon peer link and its tests (two daemons on one Mac), then the Mac UI, then the phone's approvals.

## Phase 9 — MCP browser tools  ✅ done (2026-11-01)

- ✅ MCP tools for browser panes via proxied CDP: `browser_open`, `browser_navigate`, `browser_snapshot` (an outline with refs), `browser_click`, `browser_type`, `browser_press_key`, `browser_evaluate`, `browser_screenshot`, `browser_console`, `browser_network`, `browser_wait_for`. CDP goes daemon → browser host → CEF's in-process DevTools channel (no debugging port); console and network events are buffered by the daemon from page open. Same policy as terminals: read in your Group, act in panes you opened, ask for the rest; guests and remote devices can't make DevTools calls; agents' actions are audited.
- ✅ `hmk hooks install` registers the MCP server with Claude Code (`claude mcp add --scope user`), Codex and the other agents that take one; outside a Hammock pane it offers no tools.
- ✅ Find in page (⌘F) and DevTools as a pane (Show DevTools, ⌥⌘I: Chromium's DevTools frontend, talking to its page through the browser host's DevTools channel via a localhost socket in the daemon, `internal/daemon/devtools.go`).
- ✅ Remote tier: VideoToolbox H.264 over QUIC to the iPhone and guests, drivers interact ([ADR-0012](adr/0012-remote-video-over-quic.md), [notes](internals/remote-media.md)).
- ✅ Kitty graphics in the TUI (Ghostty, Kitty): browser and simulator panes on screen drawn live from polled screenshots (JPEG from the daemon, scaled to the pane's cells and sent as PNG only when changed, Unicode placeholders, two image ids per pane so frames swap without a blank), click and drag passed through. Direct transmission rather than shared memory: the TUI often runs over SSH, and frames are already small. Follow-ups: typing into them ([#8](https://github.com/homegrownsw/hammock/issues/8)), page menus ([#9](https://github.com/homegrownsw/hammock/issues/9)), Kitty images from programs in panes ([#20](https://github.com/homegrownsw/hammock/issues/20)), shared Groups ([#21](https://github.com/homegrownsw/hammock/issues/21)), shared memory locally ([#22](https://github.com/homegrownsw/hammock/issues/22)).

## Phase 10 — Simulator panes  ✅ done (2026-11-01)

- ✅ **Spike** ([notes](internals/simulator-panes.md)): the framebuffer IOSurface through CoreSimulator and touches through SimulatorKit's HID client both work headlessly on Xcode 27.
- ✅ **Hammock Simulator** helper (`helpers/simulator`, one per daemon): the device's screen as a CAContext layer for the app, touches, buttons, typing, screenshots, device list.
- ✅ Simulator panes: daemon pane kind, Mac pane (fitted screen, click/drag to touch, typing, Home/Lock, New Simulator Tab menu), `hmk sim`, TUI placeholder (live in Ghostty and Kitty since Phase 9's TUI graphics; trying it for real is [#23](https://github.com/homegrownsw/hammock/issues/23)).
- ✅ MCP tools: `sim_list_devices`, `sim_open`, `sim_screenshot`, `sim_tap`, `sim_swipe`, `sim_type`, `sim_press_button`, `sim_install_app`, `sim_launch_app`, `sim_terminate_app`, `sim_open_url`, `sim_logs`.
- ✅ Splits: open a browser or simulator beside a pane (toolbar Split Right/Down drop-downs, pane header menus, Pane menu, palette, `hmk browser|sim --split`); merge a tab into another as a split and break a pane back out (`hmk tab merge|break`, tab and pane menus on Mac and TUI); Move to Group in the Mac tab menu.
- ✅ Device frame (DeviceHub's `.devicechrome` artwork, nine-sliced, with the device's screen mask; the power button locks; can be hidden), ⌘V/⌘C/⌘A through the device's pasteboard, non-ASCII typing by pasting.
- ✅ iOS: simulator panes as live video (the shared H.264-over-QUIC pipeline), driven by taps, swipes, typing, Home and Lock.
- ✅ Rotation and pinch through an XCTest runner per device (`helpers/simulator/runner`, made at first use from the Mac's own Xcode and started by `xcodebuild test-without-building`): `XCUIDevice` orientation and `XCSynthesizedEventRecord` fingers. The framebuffer stays in the device's portrait, so the host turns the picture (layer, screenshots, video) and maps touches back. Rotate Left/Right (⌘←/⌘→, header buttons, menus, palette, TUI, iPhone, `hmk sim rotate`, `sim_rotate`); trackpad pinch on the Mac and two-finger pinch on the iPhone, played when you let go (`hmk sim pinch`, `sim_pinch`).
- ✅ Software keyboard toggle (⇧⌘K, Indigo button 0x3f0; `hmk sim button PANE keyboard`).
- ✅ Foldables (the iPhone Duo): Open, Half Open and Close Phone (⌥⌘F, header button, menus, palette, TUI, iPhone, `hmk sim fold`, `sim_fold`) through a guest helper inside the device (`helpers/simulator/guest`: a virtual HID service sending the hinge-slider-control event locationd's device state relay reads). The pane shows whichever screen is lit (cover closed, inner open), with that screen's own frame, mask and touch target. Pinch on the inner screen: [#6](https://github.com/homegrownsw/hammock/issues/6).

## Phase 11 — Log panes, Linux host, public release  🟡 under way

- ✅ Log viewer panes (file, `log stream`, simulator logs, `adb logcat`, any command) with filtering and search (2026-11-01). From the iPhone: [#7](https://github.com/homegrownsw/hammock/issues/7).
- ✅ Linux host (2026-11-08, [linux.md](linux.md)): `hmkd`, `hmk` and the TUI cross-compiled with zig (amd64, arm64; glibc 2.28), tarballs shipped with each Mac release; `hmk service install` (systemd user unit, lingering); everything a server without a screen needs by CLI (`hmk pair` as a QR code, `hmk link pending|confirm|refuse`, `hmk status`, `--json` for scripts). The Go suite passes on Linux.
- ✅ Unraid plugin (`unraid/`): Settings ▸ Hammock over `hmk`, state and the panes' home in one folder on the pool, starting and stopping with the array (panes restored), the TUI in a window through ttyd.
- ✅ Releases (2026-11-05): Sparkle updates for the Mac, TestFlight for the iPhone, notarised builds, and release notes kept per change and frozen into each release ([release-notes/](../release-notes/README.md)).
- ✅ Ready to publish (2026-11-10): a security audit of everything a stranger's copy could reach (tech-spec §5.6; follow-ups [#46](https://github.com/homegrownsw/hammock/issues/46), [#47](https://github.com/homegrownsw/hammock/issues/47)), and acknowledgements of the open source Hammock uses (Settings ▸ About, `hmk acknowledgements`).
- Open source: the plan was open core (daemon, protocol, CLI and TUI open; the apps commercial). As built, the whole repository is open source under the MIT licence; making it public is [#38](https://github.com/homegrownsw/hammock/issues/38).
- Distribution: a DMG ([#42](https://github.com/homegrownsw/hammock/issues/42)), the update feed on GitHub Releases ([#16](https://github.com/homegrownsw/hammock/issues/16)), downloading the browser on demand ([#37](https://github.com/homegrownsw/hammock/issues/37)), moving to Applications ([#50](https://github.com/homegrownsw/hammock/issues/50)). Homebrew and apt packages, a docs site and a public beta: not started.

## Phase 12 — Paid tier: relay, accounts, SSO  not started

- E2EE relay (blind forwarder), account system, SSO.
- Thread sync between a user's hosts over the relay.
- Team management and billing.

## Built beyond the phases

Work that wasn't in a phase when the plan was written, in the order it landed.

- **Linked computers** (2026-11-02, [ADR-0013](adr/0013-linked-computers.md)): your own computers, Macs and Linux servers, see and work in each other's Groups as their owner, linked by a short-code check, with introductions and unlinking that reaches every computer. Follow-ups: [#24](https://github.com/homegrownsw/hammock/issues/24), [#31](https://github.com/homegrownsw/hammock/issues/31).
- **Screen sharing and the menu bar item** (2026-11-04, [ADR-0014](adr/0014-screen-sharing-and-menu-bar.md)): a Mac's whole screen on your iPhone or another linked computer, sharp at rest and adapting to the link, at the lock screen too; and a menu bar item for the server, there whether or not the app runs. Follow-ups: [#15](https://github.com/homegrownsw/hammock/issues/15), [#28](https://github.com/homegrownsw/hammock/issues/28), [#29](https://github.com/homegrownsw/hammock/issues/29), [#30](https://github.com/homegrownsw/hammock/issues/30).
- **Scheduled tasks** (2026-11-04): the daemon runs agents and commands on a schedule, each run in a tab of its own; on the Mac, the iPhone, the TUI, `hmk schedule` and as MCP tools. Follow-ups: [#32](https://github.com/homegrownsw/hammock/issues/32), [#33](https://github.com/homegrownsw/hammock/issues/33), [#34](https://github.com/homegrownsw/hammock/issues/34).
- **Floating panes** (2026-11-05): panes that float over a tab, dock back into it or minimise; Mac, TUI, `hmk float|dock`, MCP. On the iPhone: [#35](https://github.com/homegrownsw/hammock/issues/35).
- **Keyboard shortcuts** (2026-11-05): every Mac action's shortcut can be changed in Settings ▸ Keyboard or the config file. In the TUI and on iOS: [#17](https://github.com/homegrownsw/hammock/issues/17).
- **The Hammock Key** (2026-11-06): an SSH key in the Mac's Secure Enclave that signs only when you approve, with Touch ID or from your iPhone; Hammock's own SSH agent in panes, passing other requests on to yours (1Password and the like); signing for git and jj. Follow-ups: [#25](https://github.com/homegrownsw/hammock/issues/25), [#26](https://github.com/homegrownsw/hammock/issues/26), [#27](https://github.com/homegrownsw/hammock/issues/27).
- **Every agent in one registry** (2026-11-07, [ADR-0015](adr/0015-agent-registry.md)): Claude Code and Codex, then Gemini CLI, Qwen Code, OpenCode, Kilo, Cursor, Copilot, Droid, Amp, Pi, Crush, Grok, Kiro, Kimi Code, Aider, Goose, Cline and Antigravity ([agents.md](agents.md)). Switching a conversation between agents is still Claude Code and Codex only.
- **Remote access on any network** (2026-11-08, [ADR-0016](adr/0016-remote-access-on-any-network.md)): a local network or a VPN as well as Tailscale, with Bonjour to find computers.
- **Editor panes** (2026-11-08): a text file in a pane (CodeMirror), its buffer kept by the daemon and shared by every client, on the Mac and the iPhone; `hmk edit` and MCP editor tools.
- **Settings in a file, themes, and a new Settings window** (2026-11-08 and 9): every setting in `~/.config/hammock/config`, themes for the terminal, window and agent view (followed by the iPhone), and Settings laid out like System Settings, with search.

## Cross-cutting, every phase

- Benchmarks stay green.
- Protocol changes are additive and versioned.
- Each significant decision gets an ADR in `docs/adr/`.
- Dogfood continuously from Phase 1 onward.
