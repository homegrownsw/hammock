<p align="center">
  <img src="marketing/banner.png" alt="Hammock: terminals, coding agents, browsers and simulators, always on" width="100%">
</p>

<p align="center">
  <a href="https://hammock.jsp.scot"><b>Website</b></a> ·
  <a href="https://hammock.jsp.scot/download"><b>Download for Mac</b></a> ·
  <a href="docs/product-spec.md">Product spec</a> ·
  <a href="docs/build-plan.md">Build plan</a>
</p>

# Hammock

A persistent, agent-aware workspace: a daemon that owns your terminals, agents, browsers and simulators — organised into Groups → Tabs → Panes — with native macOS/iOS clients, a TUI, an MCP server for agents, and (later) multiplayer Groups.

| | |
|---|---|
| 🟢 **Always on** | Every pane lives in its own process: quit the app, update it or crash it, and your terminals and agents keep running. After a reboot, agents resume their sessions. |
| 🟡 **An agent cockpit** | See at a glance which agent is working, which needs you and which has finished, across every Group. |
| 🟠 **Finds you** | A notification on the Mac you're using, or on your iPhone when you're away from it. Answer from there. |
| 🔴 **More than terminals** | Chromium browser, iOS Simulator and log panes, side by side with your shells. |
| 🟣 **Agents drive it too** | Through `hmk mcp`, agents open panes, run dev servers, check pages and tap through simulators, inside their own Group. |
| 🔵 **Native, and quick** | SwiftUI on the Mac and iPhone, terminals drawn by libghostty, plus a TUI and the `hmk` CLI. |

## Build

Requirements: macOS 15+, Go 1.27+, Xcode 27.2 / Swift 6 (the project is Xcode's JSON format), `zig` 0.17 (for libghostty-vt), `protoc` + `protoc-gen-go` + `protoc-gen-swift` (only to regenerate protocol code), `just`.

```sh
just build     # Go binaries → bin/ (hmkd, pane-holder, hammock); builds libghostty-vt
               # the first time, into a cache every checkout and worktree shares
just worktree NAME  # a worktree in .claude/worktrees/NAME, ready to build
just mac-app   # dist/Hammock.app (bundles the Go binaries)
just test      # Go tests;  (cd apple && swift test) for the Swift engine tests
just bench     # latency / throughput benchmarks
```

## Use

- **Mac app:** `open dist/Hammock.app`. It starts its bundled daemon if none is running; Hammock ▸ *Run Daemon at Login* makes that permanent.
- **TUI:** `hmk tui` — sidebar of Groups/tabs, split panes, Ctrl-B prefix (`c` tab, `%`/`"` split, `o` pane, `n`/`p` tab, `x` close, `q` quit).
- **CLI:** `hmk ls`, `hmk group new NAME`, `hmk tab new GROUP -- CMD`, `hmk split PANE`, `hmk attach PANE`, `hmk send/read PANE`, `hmk audit`.
- **Agents:** status is detected automatically; for precise states install hooks with `hmk hooks install` (backs up and merges your Claude Code / Codex config).
- **MCP:** add `hmk mcp` as an MCP server for your agents to let them open, watch and drive panes (scoped to their Group; anything else asks you first). The scope is a guardrail, not a sandbox: for a real boundary, use your agent harness's own sandbox.

State lives in `~/Library/Application Support/Hammock` (override with `HMK_DIR`).

## Linux and Unraid

The daemon, `hmk` and the TUI run on Linux too, so sessions can live on a server your Mac links to:
`just linux` builds them (tarballs for amd64 and arm64), `just unraid` the Unraid plugin. Everything
(service, remote access, linking, pairing) is an `hmk` command. See [docs/linux.md](docs/linux.md).

## iOS and Xcode Cloud

- **On an iPhone:** `just ios-device` builds, signs (team in `apple/iOS/HammockIOS.xcodeproj`) and installs it.
- **Push:** the relay (`relay/push`, a Cloudflare Worker) is at https://hmk-push.homegrownsw.workers.dev. Phones built from Xcode use APNs' sandbox and TestFlight builds its production environment, so the relay's APNs key must allow both (Apple turns a key down for the wrong one with `BadEnvironmentKeyInToken`, which the daemon log shows).
- **Xcode Cloud:** the project's shared schemes are `Hammock` (iOS) and `Hammock Mac` (the `HammockMacApp` target: the same bundle as `just mac-app`, its helpers embedded and signed by a build phase from `bin/`). `apple/iOS/ci_scripts/ci_post_clone.sh` builds what a clone lacks (libghostty-vt's xcframework; for the Mac, the Go binaries, the browser host and the simulator host via `scripts/build-mac-payloads.sh`), and `ci_post_xcodebuild.sh` fails a notarized Mac build whose Developer ID export lost a helper's entitlements. Packages resolve only from `apple/Package.resolved`. Day to day the Mac is built with `just mac-app` / `just mac-dev`.

## Docs

- [Product spec](docs/product-spec.md) · [Technical spec](docs/tech-spec.md) · [Build plan](docs/build-plan.md)
- [UI direction](docs/design/ui-direction.md) · [Decision records](docs/adr/)
- [Benchmarks](docs/benchmarks.md) · [Linux and Unraid](docs/linux.md)

## Licence

Hammock's code is under the [MIT licence](LICENSE). The Hammock name and icon aren't: a fork may use the
code, but not ship under the name or with the icon. Third-party work Hammock uses is credited in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
