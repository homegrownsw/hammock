# Hammock verification map

The maintained recipes for proving Hammock's user-facing behaviour against a scratch daemon. Read this index, then follow the feature's file.

## Baseline preconditions

- A run started with `.claude/skills/verify-hammock/scripts/up.sh` (`--beta` for anything in the Mac window), its dir in `$RUN`, and `S=.claude/skills/verify-hammock/scripts`.
- `$S/doctor.sh "$RUN"` passes with no `FAIL`.
- The daemon starts empty (no Groups). Each recipe creates what it needs under Group names starting `verify-`.
- Never drive a daemon this run didn't start: no bare `hmk`, no `HMK_SOCKET` from the pane you run in.

## Driving conventions

- Every `hmk` call goes through `$S/hmk "$RUN" …`, or `$S/record.sh "$RUN" NN-name …` when it's proof.
- MCP tools (browser, simulator, and the agent side of editor and diff panes) go through `$S/mcp.sh "$RUN" NN-name TOOL 'JSON' …`, which calls them from the run's agent pane. `$S/mcp.sh "$RUN" tools --list` lists every tool and its arguments.
- Anything a recipe starts outside the daemon is registered in `RUN` for `down.sh`: a web server's pid in `http.pid`, a simulator it created in `sim.udid`. Files go in the run's `$SCRATCH` (from `run.env`).
- Address things by the IDs commands print (`gp_…`, `tb_…`, `pn_…`) or by `verify-` names, never by position in `hmk ls`.
- Panes run an explicit, rc-free command (`-- /bin/sh`, `-- /bin/zsh -f`) so their screens are reproducible and private.
- Wait for output by polling `hmk read` for an expected string (up to a few seconds), not with a fixed sleep.
- `hmk` exit codes matter: non-zero with a message on stderr is a failure, even if stdout looks plausible.

## Proof and skip reporting

- Each proof step pairs the action (`NN-*.cmd/.out/.exit`) with a read-only look at the result (`ls --json`, `read`, a window snapshot).
- Mac-window proof is the `-light.png`/`-dark.png` pair (look at it) plus an assertion on `-views.txt`.
- If an entry point can't be driven here (a real click, the iPhone), say so and name it. It's unverified, not verified some other way.
- A precondition the scratch daemon can't provide is a failure, with the reason added to that feature's Gotchas. Don't weaken the journey to make it pass.

## Feature entry contract

Each file has an H1, one paragraph on what a user sees, then exactly these H2s in order: `Sub-features`, `How to get to it (user POV)`, `Driving it with verify-hammock`, `Gotchas`.

## Features

- [Groups, tabs and panes](./groups-tabs-panes.md): create, rename, split, float, close and reopen the workspace's structure.

Pane kinds: each has its own recipe, and a change to one is proved with it.

- [Terminal panes](./terminal-panes.md): type into a pane, read its screen and scrollback, see it drawn in the window.
- [Browser panes](./browser-panes.md): open a page, read it, click, run JavaScript, screenshot (MCP).
- [Editor panes](./editor-panes.md): open a file at a line, read and select, reload after a change on disk.
- [Diff panes](./diff-panes.md): open a diff of a throwaway repo, comment on a line, reply, address.
- [Simulator panes](./simulator-panes.md): a simulator of your own, a local page in Safari, screenshot, buttons, logs, shut down.
- [Log panes](./log-panes.md): follow a file, parsed entries, filter by level.

Elsewhere:

- [The TUI](./tui.md): `hmk tui` in a terminal, its sidebar and Ctrl-B keys.
- [The Mac window](./mac-window.md): the sidebar, tabs and terminal panes as the app draws them, plus its command palette and Settings.
- [Scheduled tasks](./scheduled-tasks.md): a command on a schedule, run now, landing as a tab in its Group.
