# Hammock — Phased Build Plan

Status: Draft v0.2 · Owner: Alex Stevenson-Price · Last updated: 2026-11-08 (Linux host and Unraid plugin)
See [product-spec.md](product-spec.md) and [tech-spec.md](tech-spec.md).

Phases are an ordered backlog, not gates: we build incrementally and move between them as useful.

## Phase 0 — Foundations and spikes  ✅ done (2026-10-30)

Retire the riskiest technical unknowns before building on them.

- Monorepo scaffold (§2 of tech spec), CI for Go (darwin + linux builds), Xcode workspace skeleton.
- **Spike: libghostty-vt via cgo.** Build the static lib with Zig from a pinned commit; Go wrapper that feeds bytes, reads the screen, and serialises a snapshot that a second libghostty instance can replay to an identical screen.
- **Spike: pane-holder.** PTY + ring buffer + Unix socket; kill/restart a parent repeatedly under `yes`-style load with zero byte loss.
- **Spike: local latency path.** Minimal Swift app embedding libghostty, fed through UDS → Go → holder; measure keystroke-to-photon against Ghostty.
- `bench/` harness v0 (throughput + keystroke-to-photon) running locally.
- `proto/` v0: Hello, Subscribe, Group/Tab/Pane CRUD, Attach, Input, Output, Resize.

## Phase 1 — Daemon + thin TUI (terminal multiplexer core)  ✅ done

- `hmkd`: SQLite state store, event bus, protocol server over UDS, holder supervision, VT mirrors, snapshot + stream + resync, active-driver sizing.
- Groups / tabs / panes / splits; cwd inheritance from the last focused pane.
- Daemon restart/upgrade handover with holder reconnection.
- Reboot restore: layout, cwd, scrollback snapshot, shells respawned.
- `hmk` CLI (scriptable control) and a thin TUI: sidebar, tabs, splits, compositor over per-pane libghostty-vt.
- Templates: TOML parser, `hmk group new --template`, "save as template".

## Phase 2 — Agent awareness + MCP v1  ✅ done (incl. grant prompts + audit log)

- Harness adapters for Claude Code and Codex: detection, hook installation (`hmk hook`), launch/resume commands.
- Agent state machine; heuristic fallback detection on VT mirrors.
- Attention queue (daemon model + TUI view).
- Auto-resume of agents after reboot, with banners.
- MCP server v1 (terminal tools) with capability tokens, the group-scoped default policy, grant requests, and the audit log.
- Presence reporting from clients (groundwork for notification routing).

## Phase 3 — macOS app (the wedge)  🟡 built; in daily use

- HammockKit (Swift client SDK: protocol, models, libghostty view wrapper).
- Mac app: window chrome, Group/tab/pane UI, splits, cockpit sidebar, attention queue, quick actions (approve/deny/reply/interrupt), menu bar + Dock badges.
- Mac notifications with presence-aware routing.
- Onboarding: SMAppService daemon registration, hook installation with diff preview, CLI symlink. ✅ (Settings ▸ General installs `hmk`)
- Grant-request prompts and audit log viewer. ✅ (Settings ▸ Devices ▸ Recent Activity, `hmk audit`)
- Bench gates enforced in CI on Mac runners. ✅ (`just bench-gate`, `.github/workflows/ci.yml`; runs once the repo has a GitHub remote)
- Daemon self-update via the app. ✅ (replaces a daemon whose version differs from the bundled one)
- **⌘K command palette** ✅: a Spotlight/Raycast-style overlay above the whole window to jump between sessions (Groups, tabs, panes, agents) and run any Hammock command or action. Fuzzy search, keyboard-first, and deliberately whimsical: playful spring animations, sparkles and some flashiness on open and on confirm.
- **Group icons** ✅ (Configure Group…): set an emoji as a Group's icon (in place of the stack glyph); stored with the Group, so it persists and shows on every client.
- **Groups named after their directory** ✅: an unnamed Group shows the name of its current directory (the cwd of its last-focused pane, e.g. "Hammock"), following it as it changes, until it's renamed by hand.

## Phase 4 — iOS app + remote access  ✅ done

- ✅ Tailscale-interface listener, TLS, device pairing (QR), per-device keys and revocation.
- ✅ Push relay (Cloudflare Worker), E2EE payloads, Notification Service Extension, actionable notifications.
- ✅ iOS app: cockpit, attention queue, full terminal with accessory bar, deep links.
- ✅ Notification routing across Mac + iOS: pushes only while you're away from the Mac (locked, or idle 2 minutes), held until then and dropped if dealt with first.
- ✅ zstd compression of remote links (the phone's and guest daemons'): one stream per connection, flushed per message, so terminal output, agent view and state updates all reuse what came before (`internal/wirezstd`, negotiated as the `hammock.v1+zstd` WebSocket subprotocol; video frames go uncompressed).

## Phase 5 — Threads and agent switching  ✅ done (2026-11-01)

- ✅ Thread store (append-only, content-hashed, sync-ready schema).
- ✅ Transcript codecs for Claude Code and Codex (Codex's compressed rollouts too, and its 0.16x `exec` scripts read as commands and edits), with golden-file tests per harness version (`internal/thread/testdata/golden`: hand-made fixtures in each version's shapes; add one when a version changes its format).
- ✅ Switch flow: stop, encode, resume in the same pane; handoff-brief fallback. Tried both ways with the real CLIs.
- ✅ UI: "Continue in Codex/Claude", a carry-over report (a toast, `hmk switch`'s output), and the session finder: ⌘⇧O on the Mac, ⌃B O in the TUI, `hmk sessions` / `hmk resume`, over an index of every Claude Code and Codex session on the Mac (`internal/sessions`). On the iPhone and as an agent tool: GitHub issues.

## Phase 6 — Browser panes  ✅ done

- One browser host per daemon ("Hammock Browser", CEF): pages keep running across app quits and daemon restarts, and come back at their URL after a reboot ([ADR-0011](adr/0011-browser-panes-hidden-window-host.md)).
- Mac pane: Chromium's GPU layer via CALayerHost, raw input forwarding, address bar, back/forward/reload, popups as tabs, `<select>`/context menus/JS dialogs/file pickers shown by the app, downloads to ~/Downloads, favicons and page titles in the sidebar.
- `hmk browser URL`, `hmk nav PANE …`; placeholders in the TUI and on iOS.
- Left for later phases: find in page (⌘F), MCP browser tools and DevTools (Phase 9), remote/iOS viewing and TUI graphics.

## Phase 7 — Agent view  ✅ built on Mac and iOS (2026-10-30)

When Claude Code runs in a pane, toggle between the raw terminal and a rich conversation view, as in Claude's desktop and mobile apps. Especially for iOS, where reading and answering an agent beats driving a TUI on a phone keyboard. Claude only for now; Codex is a later phase; the TUI keeps the raw terminal.

**Data**
- Turns come from Claude Code's session file (JSONL; the hooks already report its path). The screen supplies only live state the file doesn't have yet: working, the prompt on screen.
- The daemon tails and decodes the file (building on `internal/thread`), and streams typed blocks with ids and deltas to every client: text, thinking, tool call + result, edit/diff, image, question, permission, plan, subagent. Clients render natively.
- History: the last ~40 turns, with older ones paged in on scroll.
- Files: the daemon serves a file to clients only if the session referenced it (read, written or linked), with size caps. Images render inline everywhere; paths open in the editor/Finder on the Mac and as a preview on iOS.

**Interaction**
- Native UI for Claude's question tool (options menu), permission prompts (Allow/Deny), plan approval, and interrupt.
- Answers are typed into the pane as the keystrokes the CLI expects, only after the screen shows the expected prompt. If it doesn't, nothing is sent; the card offers "Answer in terminal" (flips the pane) and the mismatch is logged.
- Composer: send while the agent works (queued, shown as pending), slash-command completion, image attachments (saved to a file and referenced), and dictation.

**Presentation**
- Native SwiftUI on Mac and iOS: a lazy list of block views; swift-markdown to AttributedString; a small code highlighter.
- Tool calls as compact rows ("Ran `go test ./...` ✓", "Edited AppModel.swift +12 −3") that expand to output or diff; consecutive calls group ("Used 6 tools"); thinking collapsed ("Thought for 8s").
- Subagents as a card with live status and the result, opening the subagent's own transcript in a sheet.
- Fonts: separate prose (default the system font) and code (default the terminal font) settings with size and line spacing, per device; iOS follows Dynamic Type.
- The toggle switches the pane in place (header control + shortcut); split the pane for side-by-side. Default view is a per-device setting (Mac: terminal, iOS: agent view), remembered per pane once toggled. A notification opens the agent view at the waiting question.

**Order (riskiest first)**
1. Spike keystroke answering (question options, permissions, plan approval) against the real Claude CLI, with screen checks.
2. The daemon's turn stream and file serving.
3. The Mac view: read-only, then interactive.
4. The iOS view.
5. Composer extras: slash completion, images, dictation.

## Phase 8 — Multiplayer Groups  ✅ done (built 2026-10-30; checked between two Macs)

Decided 2026-10-30 (grilled; see ADR-0008):

- **Reach:** Tailscale for now, nothing depending on it: an invite link lists endpoints, so a hosted relay can be added later as a blind pipe. TLS end to end with the host's pinned certificate; every connection signed with the guest Mac's key.
- **Identity:** one key per Mac; shown as the macOS account picture (coloured initials without one), the person's name and the Mac's name. Settings ▸ Sharing to change them.
- **Inviting:** one reusable link per Group until revoked or regenerated. The host approves each person once per Group, choosing View or Drive; they then reconnect on their own until removed. Approvals: Mac notification + Needs You, and the phone's Needs You while open (push later).
- **Roles:** View (watch) and Drive (type, answer agents, open/split/close/rename tabs, drop files, which upload to the host, 20 MB cap). Only the host invites and changes roles. Remove a person, stop sharing a Group, or Stop All Sharing (kill switch).
- **Guests' sidebar:** shared Groups mix in with their own, reorderable, each with the host's avatar and Mac name; dimmed with their last state while the host is unreachable.
- **Together:** presence avatars on tabs, panes and collapsed Groups, a typing glow; a pane takes the size of whoever typed last; agents needing input notify the host and Drivers, and whoever answers clears it for all.
- **Agents:** a guest's agents act in a shared Group with their person's role (Drive means Drive), within the usual agent scope.
- **Later:** GitHub issues: push for guests, follow mode, Groups spanning Macs, phone presence; the relay is Phase 12. (Browser panes in shared Groups came with remote video, Phase 9.)

Build order: the daemon peer link and its tests (two daemons on one Mac), then the Mac UI, then the phone's approvals.

## Phase 9 — MCP browser tools  ✅ built (2026-11-01)

- ✅ MCP tools for browser panes via proxied CDP: `browser_open`, `browser_navigate`, `browser_snapshot` (an outline with refs), `browser_click`, `browser_type`, `browser_press_key`, `browser_evaluate`, `browser_screenshot`, `browser_console`, `browser_network`, `browser_wait_for`. CDP goes daemon → browser host → CEF's in-process DevTools channel (no debugging port); console and network events are buffered by the daemon from page open. Same policy as terminals: read in your Group, act in panes you opened, ask for the rest; guests and remote devices can't make DevTools calls; agents' actions are audited.
- ✅ `hmk hooks install` registers the MCP server with Claude Code (`claude mcp add --scope user`) and Codex; outside a Hammock pane it offers no tools.
- ✅ Find in page (⌘F) and DevTools as a pane (Show DevTools, ⌥⌘I: Chromium's DevTools frontend, talking to its page through the browser host's DevTools channel via a localhost socket in the daemon, `internal/daemon/devtools.go`).
- ✅ Remote tier: VideoToolbox H.264 over QUIC to the iPhone and guests, drivers interact ([ADR-0012](adr/0012-remote-video-over-quic.md)).
- ✅ Kitty graphics in the TUI (Ghostty, Kitty): browser and simulator panes on screen drawn live from polled screenshots (JPEG from the daemon, scaled to the pane's cells and sent as PNG only when changed, Unicode placeholders, two image ids per pane so frames swap without a blank), click and drag passed through. Direct transmission rather than shared memory: the TUI often runs over SSH, and frames are already small; typing into them is still to do.

## Phase 10 — Simulator panes  ✅ built (rotation, pinch and the software keyboard 2026-11-01)

- ✅ **Spike** ([notes](internals/simulator-panes.md)): the framebuffer IOSurface through CoreSimulator and touches through SimulatorKit's HID client both work headlessly on Xcode 27.
- ✅ **Hammock Simulator** helper (`helpers/simulator`, one per daemon): the device's screen as a CAContext layer for the app, touches, buttons, typing, screenshots, device list.
- ✅ Simulator panes: daemon pane kind, Mac pane (fitted screen, click/drag to touch, typing, Home/Lock, New Simulator Tab menu), `hmk sim`, TUI placeholder (live in Ghostty and Kitty since Phase 9's TUI graphics).
- ✅ MCP tools: `sim_list_devices`, `sim_open`, `sim_screenshot`, `sim_tap`, `sim_swipe`, `sim_type`, `sim_press_button`, `sim_install_app`, `sim_launch_app`, `sim_terminate_app`, `sim_open_url`, `sim_logs`.
- ✅ Splits: open a browser or simulator beside a pane (toolbar Split Right/Down drop-downs, pane header menus, Pane menu, palette, `hmk browser|sim --split`); merge a tab into another as a split and break a pane back out (`hmk tab merge|break`, tab and pane menus on Mac and TUI); Move to Group in the Mac tab menu.
- ✅ Device frame (DeviceHub's `.devicechrome` artwork, nine-sliced, with the device's screen mask; the power button locks; can be hidden), ⌘V/⌘C/⌘A through the device's pasteboard, non-ASCII typing by pasting.
- ✅ iOS: simulator panes as live video (the shared H.264-over-QUIC pipeline), driven by taps, swipes, typing, Home and Lock.
- ✅ Rotation and pinch (2026-11-01) through an XCTest runner per device (`helpers/simulator/runner`, started on first use by `xcodebuild test-without-building`): `XCUIDevice` orientation and `XCSynthesizedEventRecord` fingers. The framebuffer stays in the device's portrait, so the host turns the picture (layer, screenshots, video) and maps touches back. Rotate Left/Right (⌘←/⌘→, header buttons, menus, palette, TUI, iPhone, `hmk sim rotate`, `sim_rotate`); trackpad pinch on the Mac and two-finger pinch on the iPhone, played when you let go (`hmk sim pinch`, `sim_pinch`).
- ✅ Software keyboard toggle (⇧⌘K, Indigo button 0x3f0; `hmk sim button PANE keyboard`).
- ✅ Foldables (the iPhone Duo, 2026-11-01): Open, Half Open and Close Phone (⌥⌘F, header button, menus, palette, TUI, iPhone, `hmk sim fold`, `sim_fold`) through a guest helper inside the device (`helpers/simulator/guest`: a virtual HID service sending the hinge-slider-control event locationd's device state relay reads). The pane shows whichever screen is lit (cover closed, inner open), with that screen's own frame, mask and touch target.

## Phase 11 — Log panes, Linux host, public release prep

- ✅ Log viewer panes (file, `log stream`, simulator logs, `adb logcat`, any command) with filtering and search (2026-11-01).
- ✅ Linux host (2026-11-08, [docs/linux.md](linux.md)): `hmkd`, `hmk` and the TUI cross-compiled with zig (amd64, arm64; glibc 2.28), tarballs shipped with each Mac release; `hmk service install` (systemd user unit, lingering); everything a server without a screen needs by CLI (`hmk pair` as a QR code, `hmk link pending|confirm|refuse`, `hmk status`, `--json` for scripts). The Go suite passes on Linux.
- ✅ Unraid plugin (`unraid/`): Settings ▸ Hammock over `hmk`, state and the panes' home in one folder on the pool, starting and stopping with the array (panes restored), the TUI in a window through ttyd.
- Homebrew/apt packaging, at the public release (somewhere public to point them).
- Open-sourcing the open-core components; docs site; public beta.

## Phase 12 — Paid tier: relay, accounts, SSO

- E2EE relay (blind forwarder), account system, SSO.
- Thread sync between a user's hosts over the relay.
- Team management and billing.

## Cross-cutting, every phase

- Benchmarks stay green.
- Protocol changes are additive and versioned.
- Each significant decision gets an ADR in `docs/adr/`.
- Dogfood continuously from Phase 1 onward.
